package app

import (
	"strings"
	"testing"
)

func TestValidateCreateProduct(t *testing.T) {
	valid := CreateProductInput{
		ProductID:   "APP",
		Name:        " 示例产品 ",
		GitURL:      "git@github.com:Casper-Mars/app.git",
		Description: "示范",
		License:     "MIT",
	}

	normalized, fields := validateCreateProduct(valid)
	if len(fields) != 0 {
		t.Fatalf("valid input rejected: %+v", fields)
	}
	if normalized.Name != "示例产品" {
		t.Errorf("name = %q, want trimmed", normalized.Name)
	}

	cases := []struct {
		name  string
		in    CreateProductInput
		field string
	}{
		{name: "missing id", in: CreateProductInput{Name: "n", GitURL: "g"}, field: "product_id"},
		{name: "leading digit", in: CreateProductInput{ProductID: "1APP", Name: "n", GitURL: "g"}, field: "product_id"},
		{name: "underscore first", in: CreateProductInput{ProductID: "_APP", Name: "n", GitURL: "g"}, field: "product_id"},
		{name: "too long", in: CreateProductInput{ProductID: "A" + strings.Repeat("b", 32), Name: "n", GitURL: "g"}, field: "product_id"},
		{name: "blank name", in: CreateProductInput{ProductID: "APP", Name: "  ", GitURL: "g"}, field: "name"},
		{name: "long name", in: CreateProductInput{ProductID: "APP", Name: strings.Repeat("名", 201), GitURL: "g"}, field: "name"},
		{name: "empty git url", in: CreateProductInput{ProductID: "APP", Name: "n"}, field: "git_url"},
		{name: "long git url", in: CreateProductInput{ProductID: "APP", Name: "n", GitURL: strings.Repeat("u", 501)}, field: "git_url"},
		{name: "long description", in: CreateProductInput{ProductID: "APP", Name: "n", GitURL: "g", Description: strings.Repeat("d", 2001)}, field: "description"},
		{name: "long license", in: CreateProductInput{ProductID: "APP", Name: "n", GitURL: "g", License: strings.Repeat("l", 65)}, field: "license"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, fields := validateCreateProduct(tc.in)
			if len(fields) != 1 || fields[0].Field != tc.field {
				t.Fatalf("fields = %+v, want only %q", fields, tc.field)
			}
		})
	}
}

func TestValidateUpdateProduct(t *testing.T) {
	name := "  新名字  "
	description := "新描述"
	license := "  Apache-2.0  "

	normalized, fields := validateUpdateProduct(UpdateProductInput{
		Name:        &name,
		Description: &description,
		License:     &license,
	})
	if len(fields) != 0 {
		t.Fatalf("valid patch rejected: %+v", fields)
	}
	if *normalized.Name != "新名字" || *normalized.License != "Apache-2.0" || *normalized.Description != "新描述" {
		t.Errorf("normalized = %+v", normalized)
	}

	immutable := "NEW"
	emptyName := "  "
	longLicense := strings.Repeat("l", 65)
	cases := []struct {
		name  string
		in    UpdateProductInput
		field string
	}{
		{name: "immutable id", in: UpdateProductInput{ProductID: &immutable}, field: "product_id"},
		{name: "empty patch", in: UpdateProductInput{}, field: "body"},
		{name: "blank name", in: UpdateProductInput{Name: &emptyName}, field: "name"},
		{name: "long license", in: UpdateProductInput{License: &longLicense}, field: "license"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, fields := validateUpdateProduct(tc.in)
			if len(fields) == 0 {
				t.Fatal("validation passed, want a field error")
			}
			for _, field := range fields {
				if field.Field == tc.field {
					return
				}
			}
			t.Fatalf("fields = %+v, want %q", fields, tc.field)
		})
	}
}
