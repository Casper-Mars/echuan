package domain

import (
	"errors"
	"testing"
)

// TestTypedErrorsUnwrapToSentinels pins the error contract the HTTP layer
// relies on: every typed error renders a stable message and unwraps to exactly
// one sentinel, so errors.Is works without a type switch.
func TestTypedErrorsUnwrapToSentinels(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		sentinel error
		want     string
	}{
		{
			name:     "validation",
			err:      &ValidationError{Fields: []FieldError{{Field: "title", Reason: "is required"}}},
			sentinel: ErrInvalidArgument,
			want:     "invalid argument",
		},
		{
			name:     "conflict",
			err:      &ConflictError{Reason: "product APP already exists"},
			sentinel: ErrConflict,
			want:     "product APP already exists",
		},
		{
			name:     "invalid state",
			err:      &InvalidStateError{Reason: "status done accepts nothing"},
			sentinel: ErrInvalidState,
			want:     "status done accepts nothing",
		},
		{
			name:     "invalid version",
			err:      &InvalidVersionError{Current: 3, Expected: 1},
			sentinel: ErrInvalidVersion,
			want:     "version mismatch: expected 1, current version is 3",
		},
		{
			name:     "not found with key",
			err:      &NotFoundError{Resource: "task", Key: "APP-1-T0001"},
			sentinel: ErrNotFound,
			want:     `task "APP-1-T0001" not found`,
		},
		{
			name:     "not found without key",
			err:      &NotFoundError{Resource: "project"},
			sentinel: ErrNotFound,
			want:     "project not found",
		},
		{
			name:     "too large with known size",
			err:      &TooLargeError{Field: "body_md", Limit: 10, Actual: 12},
			sentinel: ErrTooLarge,
			want:     "body_md exceeds 10 bytes (got 12 bytes)",
		},
		{
			name:     "too large without size",
			err:      &TooLargeError{Field: "body", Limit: 1024},
			sentinel: ErrTooLarge,
			want:     "body exceeds 1024 bytes",
		},
		{
			name:     "lease lost",
			err:      &LeaseLostError{TaskID: "APP-1-T0001", Reason: "lease expired"},
			sentinel: ErrLeaseLost,
			want:     `lease for task "APP-1-T0001" is lost: lease expired`,
		},
		{
			name:     "material required with field",
			err:      &MaterialRequiredError{Field: "summary_md", Reason: "must not be blank"},
			sentinel: ErrMaterialRequired,
			want:     "summary_md: must not be blank",
		},
		{
			name:     "material required without field",
			err:      &MaterialRequiredError{},
			sentinel: ErrMaterialRequired,
			want:     "material required",
		},
		{
			name:     "worker unknown",
			err:      &WorkerUnknownError{WorkerID: "w_01JBC9Z3K4QW8V0M2X7Y5N6P8R"},
			sentinel: ErrWorkerUnknown,
			want:     `worker "w_01JBC9Z3K4QW8V0M2X7Y5N6P8R" is unknown`,
		},
		{
			name:     "worker busy with task",
			err:      &WorkerBusyError{WorkerID: "w_1", TaskID: "APP-1-T0001"},
			sentinel: ErrWorkerBusy,
			want:     `worker "w_1" is busy with task "APP-1-T0001"`,
		},
		{
			name:     "worker busy with reason",
			err:      &WorkerBusyError{WorkerID: "w_1", Reason: "the card is already bound to another worker"},
			sentinel: ErrWorkerBusy,
			want:     `worker "w_1" is busy: the card is already bound to another worker`,
		},
		{
			name:     "worker busy without detail",
			err:      &WorkerBusyError{WorkerID: "w_1"},
			sentinel: ErrWorkerBusy,
			want:     `worker "w_1" is busy`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
			if !errors.Is(tc.err, tc.sentinel) {
				t.Errorf("errors.Is(%T, %v) = false", tc.err, tc.sentinel)
			}
			// A typed error must not look like a different class.
			for _, other := range []error{
				ErrInvalidArgument, ErrNotFound, ErrConflict, ErrInvalidState,
				ErrInvalidVersion, ErrTooLarge, ErrLeaseLost, ErrMaterialRequired,
				ErrWorkerUnknown, ErrWorkerBusy,
			} {
				if other == tc.sentinel {
					continue
				}
				if errors.Is(tc.err, other) {
					t.Errorf("errors.Is(%T, %v) = true, want false", tc.err, other)
				}
			}
		})
	}
}

// TestTypedErrorsAsTarget checks that errors.As recovers the concrete type and
// its payload, which is how the HTTP layer builds the error body.
func TestTypedErrorsAsTarget(t *testing.T) {
	validation := &ValidationError{
		Fields:  []FieldError{{Field: "priority", Reason: "must be <= 1000"}},
		Message: "任务拆解过粗",
	}
	var gotValidation *ValidationError
	if !errors.As(error(validation), &gotValidation) {
		t.Fatal("errors.As(ValidationError) = false")
	}
	if len(gotValidation.Fields) != 1 || gotValidation.Fields[0].Field != "priority" {
		t.Errorf("fields = %+v", gotValidation.Fields)
	}
	if gotValidation.Message != "任务拆解过粗" {
		t.Errorf("message = %q", gotValidation.Message)
	}

	version := &InvalidVersionError{Current: 7, Expected: 2}
	var gotVersion *InvalidVersionError
	if !errors.As(error(version), &gotVersion) {
		t.Fatal("errors.As(InvalidVersionError) = false")
	}
	if gotVersion.Current != 7 || gotVersion.Expected != 2 {
		t.Errorf("version payload = %+v", gotVersion)
	}

	// Wrapping with %w must keep both the typed error and the sentinel
	// reachable.
	wrapped := errors.Join(errors.New("context"), &NotFoundError{Resource: "product", Key: "APP"})
	if !errors.Is(wrapped, ErrNotFound) {
		t.Error("errors.Is(join, ErrNotFound) = false")
	}
	var gotNotFound *NotFoundError
	if !errors.As(wrapped, &gotNotFound) {
		t.Error("errors.As(join, *NotFoundError) = false")
	}
}
