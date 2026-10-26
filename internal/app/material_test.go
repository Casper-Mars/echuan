package app

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain"
)

// createRequirementProject creates one product and one project through the
// service, the way the HTTP layer does.
func createRequirementProject(t *testing.T, env *projectTestEnv, projectID string) domain.ProjectDetail {
	t.Helper()

	env.createProduct(t, "APP")
	detail, err := env.service.CreateProject(context.Background(), CreateProjectInput{
		ProductID:     "APP",
		Title:         projectID + " 需求",
		RequirementMD: sampleRequirement,
		UploadedBy:    "agent:cto",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return detail
}

func TestDownloadReturnsStoredBytes(t *testing.T) {
	env := newProjectTestEnv(t)
	detail := createRequirementProject(t, env, "APP-1")

	dl, err := NewMaterialService(env.store, env.writer).Download(context.Background(), detail.Requirement.MaterialID)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer dl.Content.Close()

	body, err := io.ReadAll(dl.Content)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(body) != sampleRequirement {
		t.Errorf("body = %q, want %q", body, sampleRequirement)
	}
	if dl.Material.SHA256 != detail.Requirement.SHA256 {
		t.Errorf("sha256 = %q, want %q", dl.Material.SHA256, detail.Requirement.SHA256)
	}
	if dl.Material.Size != int64(len(sampleRequirement)) {
		t.Errorf("size = %d, want %d", dl.Material.Size, len(sampleRequirement))
	}
	if dl.Material.Version != 1 || dl.Material.Kind != domain.MaterialKindRequirement {
		t.Errorf("material = %+v, want kind 1 version 1", dl.Material)
	}
}

func TestDownloadMissingMaterial(t *testing.T) {
	env := newProjectTestEnv(t)

	_, err := NewMaterialService(env.store, env.writer).Download(context.Background(), "NOPE")
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}

func TestDownloadRequiresID(t *testing.T) {
	env := newProjectTestEnv(t)

	_, err := NewMaterialService(env.store, env.writer).Download(context.Background(), "  ")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
}

func TestChainOrdersVersionsAndExposesBusinessIDs(t *testing.T) {
	env := newProjectTestEnv(t)
	detail := createRequirementProject(t, env, "APP-1")

	service := NewMaterialService(env.store, env.writer)
	for _, body := range []string{"# 第二版\n", "# 第三版\n"} {
		if _, err := env.service.WriteRequirement(context.Background(), "APP-1", WriteRequirementInput{
			BodyMD:     body,
			UploadedBy: "agent:cto",
		}); err != nil {
			t.Fatalf("WriteRequirement: %v", err)
		}
	}

	chain, err := service.Chain(context.Background(), "APP-1", domain.MaterialKindRequirement)
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if len(chain.Versions) != 3 {
		t.Fatalf("versions = %d, want 3", len(chain.Versions))
	}

	wantVersions := []int{1, 2, 3}
	for i, version := range chain.Versions {
		if version.Material.Version != wantVersions[i] {
			t.Errorf("versions[%d] = %d, want %d", i, version.Material.Version, wantVersions[i])
		}
	}
	if chain.Versions[0].Material.MaterialID != detail.Requirement.MaterialID {
		t.Errorf("first material = %q, want %q", chain.Versions[0].Material.MaterialID, detail.Requirement.MaterialID)
	}
	if chain.Versions[0].PreviousMaterialID != "" {
		t.Errorf("root previous = %q, want empty", chain.Versions[0].PreviousMaterialID)
	}
	for i := 1; i < len(chain.Versions); i++ {
		previous := chain.Versions[i-1].Material.MaterialID
		if chain.Versions[i].PreviousMaterialID != previous {
			t.Errorf("versions[%d].previous = %q, want %q", i, chain.Versions[i].PreviousMaterialID, previous)
		}
	}
	if chain.CurrentMaterialID != chain.Versions[len(chain.Versions)-1].Material.MaterialID {
		t.Errorf("current = %q, want the last version", chain.CurrentMaterialID)
	}
}

func TestChainEmptyWhenKindHasNoMaterial(t *testing.T) {
	env := newProjectTestEnv(t)
	createRequirementProject(t, env, "APP-1")

	chain, err := NewMaterialService(env.store, env.writer).Chain(context.Background(), "APP-1", domain.MaterialKindSpec)
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if chain.Versions == nil {
		t.Error("versions = nil, want an empty slice so JSON renders []")
	}
	if len(chain.Versions) != 0 {
		t.Errorf("versions = %d, want 0", len(chain.Versions))
	}
	if chain.CurrentMaterialID != "" {
		t.Errorf("current = %q, want empty", chain.CurrentMaterialID)
	}
}

func TestChainRejectsInvalidKind(t *testing.T) {
	env := newProjectTestEnv(t)
	createRequirementProject(t, env, "APP-1")

	for _, kind := range []int{0, 3, -1} {
		_, err := NewMaterialService(env.store, env.writer).Chain(context.Background(), "APP-1", kind)
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Errorf("kind %d: err = %v, want ValidationError", kind, err)
		}
	}
}

func TestChainRejectsMissingProject(t *testing.T) {
	env := newProjectTestEnv(t)

	_, err := NewMaterialService(env.store, env.writer).Chain(context.Background(), "NOPE-1", domain.MaterialKindRequirement)
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}
}
