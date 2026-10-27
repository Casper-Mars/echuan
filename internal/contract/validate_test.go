package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

const fixtureDocument = `
openapi: 3.0.3
info: {title: fixture, version: "1"}
paths:
  /sample:
    get:
      operationId: getSample
      summary: sample
      tags: [fixture]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Sample"}
components:
  schemas:
    Sample:
      type: object
      additionalProperties: false
      required: [name, count, when]
      properties:
        name: {type: string}
        count: {type: integer}
        when: {type: string, format: date-time}
        mode: {type: string, enum: [a, b]}
        optional: {type: string, nullable: true}
    Composed:
      allOf:
        - {$ref: "#/components/schemas/Sample"}
        - type: object
          properties:
            extra: {type: string}
`

func decodeJSON(t *testing.T, raw string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode fixture JSON %q: %v", raw, err)
	}
	return value
}

func fixture(t *testing.T) *Document {
	t.Helper()
	doc, err := Parse([]byte(fixtureDocument))
	if err != nil {
		t.Fatalf("Parse fixture: %v", err)
	}
	return doc
}

func TestValidateAcceptsCompleteResponse(t *testing.T) {
	doc := fixture(t)
	value := decodeJSON(t, `{"name":"x","count":3,"when":"2026-10-27T02:00:00.123Z","mode":"a","optional":null}`)
	if err := doc.Validate("GET", "/sample", 200, value); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidateRejectsBadResponses(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		field string
	}{
		{"missing required", `{"name":"x","when":"2026-10-27T02:00:00Z"}`, "count"},
		{"wrong type", `{"name":"x","count":"three","when":"2026-10-27T02:00:00Z"}`, "count"},
		{"extra field", `{"name":"x","count":3,"when":"2026-10-27T02:00:00Z","surprise":1}`, "surprise"},
		{"enum out of range", `{"name":"x","count":3,"when":"2026-10-27T02:00:00Z","mode":"z"}`, "mode"},
		{"bad date-time", `{"name":"x","count":3,"when":"2026-13-99 25:61"}`, "when"},
		{"non-nullable null", `{"name":null,"count":3,"when":"2026-10-27T02:00:00Z"}`, "name"},
	}
	doc := fixture(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := doc.Validate("GET", "/sample", 200, decodeJSON(t, tc.raw))
			if err == nil {
				t.Fatalf("Validate accepted %s, want failure", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error = %q, want it to mention %q", err, tc.field)
			}
		})
	}
}

func TestValidateAllOf(t *testing.T) {
	doc := fixture(t)
	base := `"name":"x","count":3,"when":"2026-10-27T02:00:00Z"`
	if err := doc.ValidateComponent("Composed", decodeJSON(t, `{`+base+`,"extra":"y"}`)); err != nil {
		t.Fatalf("allOf valid: %v", err)
	}
	if err := doc.ValidateComponent("Composed", decodeJSON(t, `{`+base+`,"extra":1}`)); err == nil {
		t.Fatal("allOf accepted wrong extra type")
	}
}

func TestParseRejectsBrokenDocuments(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"dangling ref", `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      summary: x
      tags: [x]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Missing"}
`, "dangling $ref"},
		{"unknown type", `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      summary: x
      tags: [x]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: blob}
`, "unknown type"},
		{"unsupported keyword", `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: getX
      summary: x
      tags: [x]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: string, pattern: "^x"}
`, "unsupported schema keyword"},
		{"duplicate operationId", `
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /x:
    get:
      operationId: same
      summary: x
      tags: [x]
      responses: {"200": {description: ok}}
  /y:
    get:
      operationId: same
      summary: y
      tags: [y]
      responses: {"200": {description: ok}}
`, "used by both"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			if err == nil {
				t.Fatal("Parse accepted a broken document")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
		})
	}
}
