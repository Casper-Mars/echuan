// Package contract implements the lightweight OpenAPI 3.0 contract checker
// used by the API tests. It is test/tooling only: the production binary never
// imports it, so gopkg.in/yaml.v3 is not linked into cmd/echuan.
//
// The validator deliberately supports only the vocabulary the echuan contract
// uses ($ref, type, required, properties, additionalProperties, nullable,
// enum, allOf, oneOf, items, minimum/maximum, minItems and format
// date-time/binary). Anything else is reported as an error instead of being
// silently ignored.
package contract

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Document is the parsed subset of api/openapi.yaml the checks need.
type Document struct {
	OpenAPI    string               `yaml:"openapi"`
	Info       Info                 `yaml:"info"`
	Paths      map[string]*PathItem `yaml:"paths"`
	Components Components           `yaml:"components"`
}

// Info is the document metadata.
type Info struct {
	Title   string `yaml:"title"`
	Version string `yaml:"version"`
}

// Components holds the reusable schemas.
type Components struct {
	Schemas   map[string]*Schema   `yaml:"schemas"`
	Responses map[string]*Response `yaml:"responses"`
}

// PathItem lists the operations of one path. Only the verbs the service uses
// are modeled; an unknown verb in the file would be invisible, so the checker
// pins the supported set in the document metadata instead.
type PathItem struct {
	Get    *Operation `yaml:"get"`
	Post   *Operation `yaml:"post"`
	Patch  *Operation `yaml:"patch"`
	Delete *Operation `yaml:"delete"`
	Put    *Operation `yaml:"put"`
	Head   *Operation `yaml:"head"`
}

// Operation is one method+path pair.
type Operation struct {
	OperationID string               `yaml:"operationId"`
	Summary     string               `yaml:"summary"`
	Tags        []string             `yaml:"tags"`
	Responses   map[string]*Response `yaml:"responses"`
}

// Response describes one status code.
type Response struct {
	Ref         string                `yaml:"$ref"`
	Description string                `yaml:"description"`
	Content     map[string]*MediaType `yaml:"content"`
}

// MediaType carries the response schema for a media type.
type MediaType struct {
	Schema *Schema `yaml:"schema"`
}

// AdditionalProperties models the boolean-or-schema form of the keyword.
type AdditionalProperties struct {
	Allowed bool
	Schema  *Schema
}

// UnmarshalYAML accepts both `additionalProperties: false` and
// `additionalProperties: {type: ...}`.
func (a *AdditionalProperties) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var allowed bool
		if err := value.Decode(&allowed); err != nil {
			return fmt.Errorf("additionalProperties: %w", err)
		}
		a.Allowed = allowed
		return nil
	}
	var schema Schema
	if err := value.Decode(&schema); err != nil {
		return fmt.Errorf("additionalProperties: %w", err)
	}
	a.Allowed = true
	a.Schema = &schema
	return nil
}

// Schema is the JSON-Schema subset used by the contract.
type Schema struct {
	Ref                  string                `yaml:"$ref"`
	Type                 string                `yaml:"type"`
	Format               string                `yaml:"format"`
	Required             []string              `yaml:"required"`
	Properties           map[string]*Schema    `yaml:"properties"`
	Items                *Schema               `yaml:"items"`
	AdditionalProperties *AdditionalProperties `yaml:"additionalProperties"`
	Nullable             bool                  `yaml:"nullable"`
	Enum                 []any                 `yaml:"enum"`
	Minimum              *float64              `yaml:"minimum"`
	Maximum              *float64              `yaml:"maximum"`
	MinItems             *int                  `yaml:"minItems"`
	AllOf                []*Schema             `yaml:"allOf"`
	OneOf                []*Schema             `yaml:"oneOf"`
}

// schemaKeywords is the exact vocabulary the checker understands. A keyword
// outside this set is a hard error: silently ignoring a constraint would make
// a green contract result meaningless.
var schemaKeywords = map[string]bool{
	"$ref": true, "type": true, "format": true, "required": true,
	"properties": true, "items": true, "additionalProperties": true,
	"nullable": true, "enum": true, "minimum": true, "maximum": true,
	"minItems": true, "allOf": true, "oneOf": true,
}

// UnmarshalYAML decodes a schema and rejects keywords the validator would not
// enforce.
func (s *Schema) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("schema must be a mapping, got kind %d", value.Kind)
	}
	var raw map[string]yaml.Node
	if err := value.Decode(&raw); err != nil {
		return err
	}
	for keyword := range raw {
		if !schemaKeywords[keyword] {
			return fmt.Errorf("unsupported schema keyword %q", keyword)
		}
	}
	type plain Schema
	var decoded plain
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*s = Schema(decoded)
	return nil
}

// OperationRef identifies one operation in the document.
type OperationRef struct {
	Method      string
	Path        string
	OperationID string
}

// Load reads and parses an OpenAPI document from disk.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses and structurally checks an OpenAPI document.
func Parse(data []byte) (*Document, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	if err := doc.check(); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Operations returns every method+path pair in a deterministic order.
func (d *Document) Operations() []OperationRef {
	var ops []OperationRef
	for path, item := range d.Paths {
		for _, entry := range item.entries() {
			ops = append(ops, OperationRef{Method: entry.method, Path: path, OperationID: entry.op.OperationID})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops
}

// Operation looks up one method+path pair.
func (d *Document) Operation(method, path string) (*Operation, bool) {
	item, ok := d.Paths[path]
	if !ok {
		return nil, false
	}
	switch strings.ToUpper(method) {
	case "GET":
		return item.Get, item.Get != nil
	case "POST":
		return item.Post, item.Post != nil
	case "PATCH":
		return item.Patch, item.Patch != nil
	case "DELETE":
		return item.Delete, item.Delete != nil
	case "PUT":
		return item.Put, item.Put != nil
	case "HEAD":
		return item.Head, item.Head != nil
	}
	return nil, false
}

type operationEntry struct {
	method string
	op     *Operation
}

func (p *PathItem) entries() []operationEntry {
	if p == nil {
		return nil
	}
	var out []operationEntry
	for _, e := range []operationEntry{
		{"GET", p.Get}, {"POST", p.Post}, {"PATCH", p.Patch},
		{"DELETE", p.Delete}, {"PUT", p.Put}, {"HEAD", p.Head},
	} {
		if e.op != nil {
			out = append(out, operationEntry{method: e.method, op: e.op})
		}
	}
	return out
}

// check validates the document structure: version, unique operation ids,
// non-empty summaries/tags/responses and a resolvable schema graph.
func (d *Document) check() error {
	if !strings.HasPrefix(d.OpenAPI, "3.0") {
		return fmt.Errorf("openapi: unexpected version %q, want 3.0.x", d.OpenAPI)
	}
	if d.Info.Title == "" || d.Info.Version == "" {
		return fmt.Errorf("info: title and version are required")
	}
	if len(d.Paths) == 0 {
		return fmt.Errorf("paths: document has no paths")
	}

	seen := map[string]string{}
	for _, op := range d.Operations() {
		if op.OperationID == "" {
			return fmt.Errorf("%s %s: operationId is empty", op.Method, op.Path)
		}
		if previous, dup := seen[op.OperationID]; dup {
			return fmt.Errorf("operationId %q is used by both %s and %s", op.OperationID, previous, op.Method+" "+op.Path)
		}
		seen[op.OperationID] = op.Method + " " + op.Path

		operation, _ := d.Operation(op.Method, op.Path)
		if operation.Summary == "" {
			return fmt.Errorf("%s %s: summary is empty", op.Method, op.Path)
		}
		if len(operation.Tags) == 0 {
			return fmt.Errorf("%s %s: tags are empty", op.Method, op.Path)
		}
		if len(operation.Responses) == 0 {
			return fmt.Errorf("%s %s: no responses", op.Method, op.Path)
		}
		for status, response := range operation.Responses {
			resolved, err := d.resolveResponse(response)
			if err != nil {
				return fmt.Errorf("%s %s: response %s: %w", op.Method, op.Path, status, err)
			}
			if resolved == nil || resolved.Description == "" {
				return fmt.Errorf("%s %s: response %s has no description", op.Method, op.Path, status)
			}
			if _, err := strconv.Atoi(status); err != nil && status != "default" {
				return fmt.Errorf("%s %s: response key %q is not a status code", op.Method, op.Path, status)
			}
			for mediaType, media := range resolved.Content {
				if media == nil || media.Schema == nil {
					return fmt.Errorf("%s %s %s %s: media type has no schema", op.Method, op.Path, status, mediaType)
				}
			}
		}
	}

	names := make([]string, 0, len(d.Components.Schemas))
	for name := range d.Components.Schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := d.checkSchema(d.Components.Schemas[name], "#/components/schemas/"+name, map[string]bool{}); err != nil {
			return err
		}
	}
	for _, op := range d.Operations() {
		operation, _ := d.Operation(op.Method, op.Path)
		for status, response := range operation.Responses {
			resolved, err := d.resolveResponse(response)
			if err != nil {
				return fmt.Errorf("%s %s: response %s: %w", op.Method, op.Path, status, err)
			}
			for _, media := range resolved.Content {
				if err := d.checkSchema(media.Schema, op.Method+" "+op.Path+" "+status, map[string]bool{}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var validTypes = map[string]bool{
	"": true, "object": true, "array": true, "string": true,
	"integer": true, "number": true, "boolean": true,
}

var validFormats = map[string]bool{"": true, "date-time": true, "binary": true}

func (d *Document) checkSchema(s *Schema, where string, stack map[string]bool) error {
	if s == nil {
		return fmt.Errorf("%s: schema is null", where)
	}
	if s.Ref != "" {
		name, err := d.refName(s.Ref)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if stack[name] {
			return fmt.Errorf("%s: cyclic $ref %q", where, s.Ref)
		}
		target, ok := d.Components.Schemas[name]
		if !ok {
			return fmt.Errorf("%s: dangling $ref %q", where, s.Ref)
		}
		stack[name] = true
		err = d.checkSchema(target, where+" -> "+name, stack)
		delete(stack, name)
		return err
	}
	if !validTypes[s.Type] {
		return fmt.Errorf("%s: unknown type %q", where, s.Type)
	}
	if !validFormats[s.Format] {
		return fmt.Errorf("%s: unsupported format %q", where, s.Format)
	}
	if s.Type == "" && len(s.AllOf) == 0 && len(s.OneOf) == 0 {
		return fmt.Errorf("%s: schema has no type", where)
	}
	for _, name := range s.Required {
		if _, ok := s.Properties[name]; !ok {
			return fmt.Errorf("%s: required field %q has no property definition", where, name)
		}
	}
	for name, property := range s.Properties {
		if err := d.checkSchema(property, where+"."+name, stack); err != nil {
			return err
		}
	}
	if s.Items != nil {
		if err := d.checkSchema(s.Items, where+"[]", stack); err != nil {
			return err
		}
	}
	for i, sub := range s.AllOf {
		if err := d.checkSchema(sub, fmt.Sprintf("%s.allOf[%d]", where, i), stack); err != nil {
			return err
		}
	}
	for i, sub := range s.OneOf {
		if err := d.checkSchema(sub, fmt.Sprintf("%s.oneOf[%d]", where, i), stack); err != nil {
			return err
		}
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		if err := d.checkSchema(s.AdditionalProperties.Schema, where+"{}", stack); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) refName(ref string) (string, error) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("unsupported $ref %q (only %s*)", ref, prefix)
	}
	name := strings.TrimPrefix(ref, prefix)
	if name == "" {
		return "", fmt.Errorf("empty $ref name")
	}
	return name, nil
}

// Validate checks one JSON response value against the schema of method+path
// and status. A response without an application/json schema is accepted only
// when it has no content at all; the caller handles binary bodies itself.
func (d *Document) Validate(method, path string, status int, value any) error {
	op, ok := d.Operation(method, path)
	if !ok {
		return fmt.Errorf("%s %s: operation not found", method, path)
	}
	rawResponse, ok := op.Responses[strconv.Itoa(status)]
	if !ok {
		return fmt.Errorf("%s %s: status %d not documented", method, path, status)
	}
	response, err := d.resolveResponse(rawResponse)
	if err != nil {
		return fmt.Errorf("%s %s %d: %w", method, path, status, err)
	}
	media, ok := response.Content["application/json"]
	if !ok {
		if len(response.Content) == 0 {
			if value != nil {
				return fmt.Errorf("%s %s %d: response has no content but a body was returned", method, path, status)
			}
			return nil
		}
		return fmt.Errorf("%s %s %d: response has no application/json schema", method, path, status)
	}
	if err := d.validate(media.Schema, value, "$", 0); err != nil {
		return fmt.Errorf("%s %s %d: %w", method, path, status, err)
	}
	return nil
}

// ValidateComponent checks a value against a named component schema (used for
// the 404/405 envelope, which is not an operation response).
func (d *Document) ValidateComponent(name string, value any) error {
	schema, ok := d.Components.Schemas[name]
	if !ok {
		return fmt.Errorf("component %q not found", name)
	}
	if err := d.validate(schema, value, "$", 0); err != nil {
		return fmt.Errorf("component %s: %w", name, err)
	}
	return nil
}

func (d *Document) validate(s *Schema, value any, path string, depth int) error {
	return d.validateWith(s, value, path, depth, nil)
}

// validateWith carries the property names declared by sibling allOf branches,
// so `additionalProperties: false` on one branch does not reject the fields a
// sibling branch contributes.
func (d *Document) validateWith(s *Schema, value any, path string, depth int, extraAllowed map[string]bool) error {
	if depth > 100 {
		return fmt.Errorf("%s: schema nesting too deep", path)
	}
	if s == nil {
		return fmt.Errorf("%s: nil schema", path)
	}
	if s.Ref != "" {
		if value == nil && s.Nullable {
			return nil
		}
		name, err := d.refName(s.Ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		target, ok := d.Components.Schemas[name]
		if !ok {
			return fmt.Errorf("%s: dangling $ref %q", path, s.Ref)
		}
		return d.validateWith(target, value, path, depth+1, extraAllowed)
	}
	if len(s.OneOf) > 0 {
		var firstErr error
		matches := 0
		for _, option := range s.OneOf {
			if err := d.validateWith(option, value, path, depth+1, extraAllowed); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			matches++
		}
		if matches != 1 {
			return fmt.Errorf("%s: matches %d oneOf branches, want exactly 1 (first error: %v)", path, matches, firstErr)
		}
		return nil
	}
	if len(s.AllOf) > 0 {
		allowed := map[string]bool{}
		for name := range extraAllowed {
			allowed[name] = true
		}
		for _, sub := range s.AllOf {
			for name := range d.propertyNames(sub) {
				allowed[name] = true
			}
		}
		for _, sub := range s.AllOf {
			if err := d.validateWith(sub, value, path, depth+1, allowed); err != nil {
				return err
			}
		}
	}

	if value == nil {
		if s.Nullable {
			return nil
		}
		return fmt.Errorf("%s: is null but schema is not nullable", path)
	}
	if err := d.checkEnum(s.Enum, value, path); err != nil {
		return err
	}

	switch s.Type {
	case "":
		// allOf-only schema with no additional constraints.
		return nil
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: is %T, want object", path, value)
		}
		for _, name := range s.Required {
			if _, present := object[name]; !present {
				return fmt.Errorf("%s: missing required field %q", path, name)
			}
		}
		for name, member := range object {
			if property, ok := s.Properties[name]; ok {
				if err := d.validate(property, member, path+"."+name, depth+1); err != nil {
					return err
				}
				continue
			}
			if extraAllowed[name] {
				continue
			}
			if s.AdditionalProperties == nil {
				continue
			}
			if !s.AdditionalProperties.Allowed {
				return fmt.Errorf("%s: unexpected field %q (additionalProperties: false)", path, name)
			}
			if s.AdditionalProperties.Schema != nil {
				if err := d.validate(s.AdditionalProperties.Schema, member, path+"."+name, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: is %T, want array", path, value)
		}
		if s.MinItems != nil && len(items) < *s.MinItems {
			return fmt.Errorf("%s: has %d items, want at least %d", path, len(items), *s.MinItems)
		}
		for i, item := range items {
			if err := d.validate(s.Items, item, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: is %T, want string", path, value)
		}
		if s.Format == "date-time" {
			if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
				return fmt.Errorf("%s: %q is not a valid date-time: %v", path, text, err)
			}
		}
		return nil
	case "integer":
		number, err := numeric(value)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		if math.Trunc(number) != number {
			return fmt.Errorf("%s: is %v, want integer", path, number)
		}
		return d.checkBounds(s, number, path)
	case "number":
		number, err := numeric(value)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		return d.checkBounds(s, number, path)
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: is %T, want boolean", path, value)
		}
		return nil
	default:
		return fmt.Errorf("%s: unknown schema type %q", path, s.Type)
	}
}

// resolveResponse follows a response-level $ref into components.responses.
func (d *Document) resolveResponse(response *Response) (*Response, error) {
	if response == nil {
		return nil, fmt.Errorf("response is null")
	}
	if response.Ref == "" {
		return response, nil
	}
	const prefix = "#/components/responses/"
	if !strings.HasPrefix(response.Ref, prefix) {
		return nil, fmt.Errorf("unsupported response $ref %q", response.Ref)
	}
	name := strings.TrimPrefix(response.Ref, prefix)
	target, ok := d.Components.Responses[name]
	if !ok {
		return nil, fmt.Errorf("dangling response $ref %q", response.Ref)
	}
	return target, nil
}

// propertyNames collects the property names declared by a schema, following
// $ref and nested allOf branches. It only inspects the current object level.
func (d *Document) propertyNames(s *Schema) map[string]bool {
	names := map[string]bool{}
	if s == nil {
		return names
	}
	if s.Ref != "" {
		name, err := d.refName(s.Ref)
		if err != nil {
			return names
		}
		return d.propertyNames(d.Components.Schemas[name])
	}
	for name := range s.Properties {
		names[name] = true
	}
	for _, sub := range s.AllOf {
		for name := range d.propertyNames(sub) {
			names[name] = true
		}
	}
	return names
}

func (d *Document) checkBounds(s *Schema, number float64, path string) error {
	if s.Minimum != nil && number < *s.Minimum {
		return fmt.Errorf("%s: %v is below minimum %v", path, number, *s.Minimum)
	}
	if s.Maximum != nil && number > *s.Maximum {
		return fmt.Errorf("%s: %v is above maximum %v", path, number, *s.Maximum)
	}
	return nil
}

func (d *Document) checkEnum(enum []any, value any, path string) error {
	if len(enum) == 0 {
		return nil
	}
	for _, allowed := range enum {
		if valueEqual(allowed, value) {
			return nil
		}
	}
	return fmt.Errorf("%s: %v is not one of %v", path, value, enum)
}

func valueEqual(a, b any) bool {
	an, aok := asNumber(a)
	bn, bok := asNumber(b)
	if aok && bok {
		return an == bn
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func numeric(value any) (float64, error) {
	switch n := value.(type) {
	case interface{ Int64() (int64, error) }:
		parsed, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("is %v, want number", value)
		}
		return float64(parsed), nil
	case interface{ Float64() (float64, error) }:
		parsed, err := n.Float64()
		if err != nil {
			return 0, fmt.Errorf("is %v, want number", value)
		}
		return parsed, nil
	}
	if n, ok := asNumber(value); ok {
		return n, nil
	}
	return 0, fmt.Errorf("is %T, want number", value)
}
