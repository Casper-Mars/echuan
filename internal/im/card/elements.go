package card

// The wire structures below are the minimum needed for the cards this package
// renders. Keeping them explicit (instead of map[string]any) makes a typo a
// compile error rather than a runtime 200530 from Feishu.

type card struct {
	Schema string     `json:"schema"`
	Config cardConfig `json:"config"`
	Header cardHeader `json:"header"`
	Body   cardBody   `json:"body"`
}

type cardConfig struct {
	// UpdateMulti must be true both when sending and when patching, otherwise
	// Feishu refuses to update a card that was already delivered.
	UpdateMulti bool `json:"update_multi"`
}

type cardHeader struct {
	Title text `json:"title"`
	// Subtitle is the second header line ("<project> · <card type>"). It is
	// plain_text as well: the title/subtitle never go through markdown parsing,
	// so a task title containing `*`, `_`, `[` or a line break is shown verbatim.
	Subtitle *text  `json:"subtitle,omitempty"`
	Template string `json:"template,omitempty"`
}

type cardBody struct {
	Elements []any `json:"elements"`
}

type text struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

func plainText(content string) text {
	return text{Tag: "plain_text", Content: content}
}

type markdownElement struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
	// TextSize is empty for the body and "notation" (12px) for the footer that
	// carries the raw task / report ids.
	TextSize string `json:"text_size,omitempty"`
}

type formElement struct {
	Tag      string `json:"tag"`
	Name     string `json:"name"`
	Elements []any  `json:"elements"`
}

type inputElement struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Placeholder text   `json:"placeholder"`
}

type columnSetElement struct {
	Tag     string          `json:"tag"`
	Columns []columnElement `json:"columns"`
}

type columnElement struct {
	Tag      string `json:"tag"`
	Width    string `json:"width"`
	Elements []any  `json:"elements"`
}

type buttonElement struct {
	Tag            string            `json:"tag"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	FormActionType string            `json:"form_action_type,omitempty"`
	Text           text              `json:"text"`
	Behaviors      []behaviorElement `json:"behaviors,omitempty"`
}

type behaviorElement struct {
	Type       string `json:"type"`
	DefaultURL string `json:"default_url"`
}
