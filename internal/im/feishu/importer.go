package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const apiBase = "https://open.feishu.cn/open-apis"

// Importer converts a Markdown document into a Feishu docx.
//
// The import flow is three calls (upload → create task → poll) and the uploaded
// file token expires after five minutes, so all three run inside one method.
type Importer struct {
	appID     string
	appSecret string
	folder    string
	http      *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewImporter builds an importer that drops documents into folderToken.
func NewImporter(appID, appSecret, folderToken string) *Importer {
	return &Importer{
		appID: appID, appSecret: appSecret, folder: folderToken,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// ImportMarkdown uploads content as name.md and imports it as a docx, returning
// the document URL.
func (im *Importer) ImportMarkdown(ctx context.Context, name string, content []byte) (string, error) {
	if im.folder == "" {
		return "", fmt.Errorf("feishu: doc folder token is empty")
	}
	fileToken, err := im.upload(ctx, name+".md", content)
	if err != nil {
		return "", err
	}
	ticket, err := im.createTask(ctx, name, fileToken)
	if err != nil {
		return "", err
	}
	return im.wait(ctx, ticket)
}

func (im *Importer) upload(ctx context.Context, fileName string, content []byte) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	fields := map[string]string{
		"file_name":   fileName,
		"parent_type": "ccm_import_open",
		"size":        fmt.Sprintf("%d", len(content)),
		"extra":       `{"obj_type":"docx","file_extension":"md"}`,
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return "", fmt.Errorf("feishu: build upload form: %w", err)
		}
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return "", fmt.Errorf("feishu: build upload file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", fmt.Errorf("feishu: write upload body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("feishu: close upload form: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/drive/v1/medias/upload_all", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := im.do(req)
	if err != nil {
		return "", err
	}
	var data struct {
		FileToken string `json:"file_token"`
	}
	if err := json.Unmarshal(resp, &data); err != nil || data.FileToken == "" {
		return "", fmt.Errorf("feishu: upload returned no file token: %s", string(resp))
	}
	return data.FileToken, nil
}

func (im *Importer) createTask(ctx context.Context, name, fileToken string) (string, error) {
	payload := map[string]any{
		"file_extension": "md",
		"file_token":     fileToken,
		"type":           "docx",
		"file_name":      name,
		"point":          map[string]any{"mount_type": 1, "mount_key": im.folder},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/drive/v1/import_tasks", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := im.do(req)
	if err != nil {
		return "", err
	}
	var data struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(resp, &data); err != nil || data.Ticket == "" {
		return "", fmt.Errorf("feishu: import task returned no ticket: %s", string(resp))
	}
	return data.Ticket, nil
}

// wait polls the import task until it succeeds. job_status: 0 done, 1 failed,
// 2 processing.
func (im *Importer) wait(ctx context.Context, ticket string) (string, error) {
	deadline := time.Now().Add(45 * time.Second)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("feishu: import task %s timed out", ticket)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/drive/v1/import_tasks/"+url.PathEscape(ticket), nil)
		if err != nil {
			return "", err
		}
		resp, err := im.do(req)
		if err != nil {
			return "", err
		}
		var data struct {
			Result struct {
				JobStatus int    `json:"job_status"`
				JobError  string `json:"job_error_msg"`
				URL       string `json:"url"`
			} `json:"result"`
		}
		if err := json.Unmarshal(resp, &data); err != nil {
			return "", fmt.Errorf("feishu: decode import result: %w", err)
		}
		switch data.Result.JobStatus {
		case 0:
			if data.Result.URL == "" {
				return "", fmt.Errorf("feishu: import succeeded without a url")
			}
			return data.Result.URL, nil
		case 1:
			return "", fmt.Errorf("feishu: import failed: %s", data.Result.JobError)
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

// do runs a request with a cached tenant token and unwraps the envelope.
func (im *Importer) do(req *http.Request) (json.RawMessage, error) {
	token, err := im.tenantToken(req.Context())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := im.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("feishu: read response: %w", err)
	}
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("feishu: decode response: %w (body=%s)", err, string(body))
	}
	if envelope.Code != 0 {
		return nil, fmt.Errorf("feishu: %s %s: code=%d msg=%s", req.Method, req.URL.Path, envelope.Code, envelope.Msg)
	}
	return envelope.Data, nil
}

// tenantToken returns a cached token, refreshing it 5 minutes before expiry.
func (im *Importer) tenantToken(ctx context.Context) (string, error) {
	im.mu.Lock()
	if im.token != "" && time.Now().Before(im.expires) {
		token := im.token
		im.mu.Unlock()
		return token, nil
	}
	im.mu.Unlock()

	payload, _ := json.Marshal(map[string]string{"app_id": im.appID, "app_secret": im.appSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/auth/v3/tenant_access_token/internal", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := im.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu: tenant token: %w", err)
	}
	defer resp.Body.Close()

	var data struct {
		Code  int    `json:"code"`
		Msg   string `json:"msg"`
		Token string `json:"tenant_access_token"`
		TTL   int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("feishu: decode tenant token: %w", err)
	}
	if data.Code != 0 || data.Token == "" {
		return "", fmt.Errorf("feishu: tenant token code=%d msg=%s", data.Code, data.Msg)
	}

	im.mu.Lock()
	im.token = data.Token
	im.expires = time.Now().Add(time.Duration(data.TTL)*time.Second - 5*time.Minute)
	im.mu.Unlock()
	return data.Token, nil
}
