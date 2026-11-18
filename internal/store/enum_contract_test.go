package store

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Casper-Mars/echuan/internal/domain/enum"
)

// EnumSQLContract is the other half of the enum contract: the Go constants in
// internal/domain/enum and the CHECK constraints in migrations/0001_init.sql
// must contain exactly the same value set. Adding a value in one place and
// forgetting the other is the failure mode this test exists for.
type EnumSQLContract struct {
	Table  string
	Column string
	Values []int
}

// ints converts an enum value set into plain ints for comparison.
func ints[T ~int](values []T) []int {
	out := make([]int, len(values))
	for i, v := range values {
		out[i] = int(v)
	}
	return out
}

// TestEnumSQLContract compares every persisted enum against its CHECK clause.
func TestEnumSQLContract(t *testing.T) {
	st := openTestStore(t, 2)

	contracts := []EnumSQLContract{
		{Table: "projects", Column: "status", Values: ints(enum.ProjectStatuses())},
		{Table: "tasks", Column: "kind", Values: ints(enum.TaskKinds())},
		{Table: "tasks", Column: "status", Values: ints(enum.TaskStatuses())},
		{Table: "materials", Column: "kind", Values: ints(enum.MaterialKinds())},
		{Table: "attachments", Column: "role", Values: ints(enum.AttachmentRoles())},
		{Table: "gate_decisions", Column: "decision", Values: ints(enum.Decisions())},
		{Table: "task_events", Column: "event", Values: ints(enum.Events())},
		{Table: "workers", Column: "state", Values: ints(enum.WorkerStates())},
		{Table: "im_card", Column: "subject_kind", Values: ints(enum.SubjectKinds())},
		{Table: "worker_kinds", Column: "kind", Values: ints(enum.TaskKinds())},
	}

	for _, contract := range contracts {
		t.Run(contract.Table+"."+contract.Column, func(t *testing.T) {
			sql := tableSQL(t, st, contract.Table)
			got := checkValues(t, sql, contract)

			want := append([]int(nil), contract.Values...)
			sort.Ints(want)
			sort.Ints(got)

			if len(got) != len(want) {
				t.Fatalf("SQL values = %v, Go values = %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("SQL values = %v, Go values = %v", got, want)
				}
			}
			for _, v := range got {
				if v == 0 {
					t.Fatalf("%s.%s accepts 0, which must be illegal", contract.Table, contract.Column)
				}
			}
		})
	}
}

// tableSQL returns the CREATE TABLE statement from sqlite_master.
func tableSQL(t *testing.T, st *Store, table string) string {
	t.Helper()
	var sql string
	err := st.Read().QueryRowContext(context.Background(),
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&sql)
	if err != nil {
		t.Fatalf("read schema of %s: %v", table, err)
	}
	return sql
}

// checkValues extracts the integer set from either
//
//	CHECK (col IN (1, 2, 3))
//	CHECK (col BETWEEN 1 AND 6)
func checkValues(t *testing.T, tableSQL string, contract EnumSQLContract) []int {
	t.Helper()

	column := regexp.QuoteMeta(contract.Column)
	inPattern := regexp.MustCompile(`(?is)CHECK\s*\(\s*` + column + `\s+IN\s*\(([^)]*)\)\s*\)`)
	betweenPattern := regexp.MustCompile(`(?is)CHECK\s*\(\s*` + column + `\s+BETWEEN\s+(\d+)\s+AND\s+(\d+)\s*\)`)

	if match := inPattern.FindStringSubmatch(tableSQL); match != nil {
		parts := strings.Split(match[1], ",")
		values := make([]int, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.Atoi(part)
			if err != nil {
				t.Fatalf("%s.%s: non-integer CHECK value %q", contract.Table, contract.Column, part)
			}
			values = append(values, n)
		}
		return values
	}

	if match := betweenPattern.FindStringSubmatch(tableSQL); match != nil {
		low, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("%s.%s: bad BETWEEN lower bound %q", contract.Table, contract.Column, match[1])
		}
		high, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatalf("%s.%s: bad BETWEEN upper bound %q", contract.Table, contract.Column, match[2])
		}
		values := make([]int, 0, high-low+1)
		for v := low; v <= high; v++ {
			values = append(values, v)
		}
		return values
	}

	t.Fatalf("%s.%s: no CHECK (col IN (...)) or CHECK (col BETWEEN ...) found", contract.Table, contract.Column)
	return nil
}
