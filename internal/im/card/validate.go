package card

import (
	"encoding/json"
	"fmt"
	"sort"
)

// validateNames walks the encoded card and enforces the two rules Feishu
// applies to interactive components: every name must be present and unique
// within the card. Violations surface as 200530 at click time, which is far
// more expensive to debug than a failed render.
func validateNames(raw []byte) error {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("card: decode for validation: %w", err)
	}

	seen := map[string]int{}
	collectNames(doc, seen)

	var duplicates []string
	for name, count := range seen {
		if name == "" {
			return fmt.Errorf("card: component with empty name")
		}
		if count > 1 {
			duplicates = append(duplicates, name)
		}
	}
	if len(duplicates) > 0 {
		sort.Strings(duplicates)
		return fmt.Errorf("card: duplicate component names %v", duplicates)
	}
	return nil
}

func collectNames(node any, seen map[string]int) {
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			if key == "name" {
				if s, ok := value.(string); ok {
					seen[s]++
				}
			}
			collectNames(value, seen)
		}
	case []any:
		for _, item := range v {
			collectNames(item, seen)
		}
	}
}
