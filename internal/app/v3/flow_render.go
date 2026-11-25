package v3

import (
	"strconv"
	"strings"
)

// renderValues are the values a card template is rendered with. The variable
// vocabulary is owned by the configuration (platform.CardTemplateVars /
// platform.ItemTemplateVars, whitelisted by R10); an unknown variable never
// reaches rendering because the flow is validated at startup.
//
// Item variables (条目标题/条目key/条目序号) are only meaningful for list and
// fanout derivations; for a single derivation the entries are empty, which
// renders them as empty strings.
type renderValues struct {
	upstreamID    string
	upstreamTitle string
	projectName   string
	productName   string
	stageName     string
	itemTitle     string
	itemKey       string
	itemSeq       int
}

// render replaces every known variable in one pass. A single pass is what keeps
// a value that itself looks like a placeholder ("{上游卡号}" inside a card
// title) from being substituted twice (D5″).
func (v renderValues) render(template string) string {
	replacer := strings.NewReplacer(
		"{上游卡号}", v.upstreamID,
		"{上游卡标题}", v.upstreamTitle,
		"{项目名}", v.projectName,
		"{产品名}", v.productName,
		"{环节名}", v.stageName,
		"{条目标题}", v.itemTitle,
		"{条目key}", v.itemKey,
		"{条目序号}", strconv.Itoa(v.itemSeq),
	)
	return replacer.Replace(template)
}
