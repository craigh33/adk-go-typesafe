package contextfilter

import (
	"reflect"
	"slices"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/craigh33/adk-go-typesafe/internal/mappers"
)

func (p *contextFilter) newConversation(ctx agent.Context, request *model.LLMRequest) conversation {
	c := conversation{contents: request.Contents}
	input := ctx.UserContent()
	current := mappers.ContentText(input)
	if current.Incomplete || !isConversationContent(input) ||
		!slices.ContainsFunc(input.Parts, func(part *genai.Part) bool {
			return part != nil && !part.Thought && strings.TrimSpace(part.Text) != ""
		}) {
		return c
	}
	c.latestRequest = current.Text
	if request.Config != nil && request.Config.SystemInstruction != nil {
		instructions := mappers.ContentText(request.Config.SystemInstruction)
		if instructions.Incomplete {
			return c
		}
		c.instructions = instructions.Text
	}
	active := -1
	for i, content := range c.contents {
		if content != nil && reflect.DeepEqual(content, input) {
			active = i
		}
	}
	// A transformed or delegated input cannot safely establish the active turn.
	if active < 0 {
		return c
	}
	for i, content := range c.contents {
		boundary := i <= active && isUserMessage(content)
		if len(c.turns) == 0 || boundary {
			c.turns = append(c.turns, turn{start: i, pinned: !boundary})
		}
		g := &c.turns[len(c.turns)-1]
		projection := mappers.ContentText(content)
		g.end = i + 1
		g.text += projection.Text + "\n"
		g.pinned = g.pinned || i >= active || projection.Incomplete || !isConversationContent(content) ||
			(p.cfg.Pin != nil && p.cfg.Pin(ctx, content))
	}
	for i := max(0, len(c.turns)-p.cfg.KeepRecentTurns); i < len(c.turns); i++ {
		c.turns[i].pinned = true
	}
	c.protectSummaries(ctx.WithICDelta(nil))
	c.protectToolPairs()
	return c
}

func (c *conversation) protectSummaries(ctx agent.InvocationContext) {
	if ctx == nil {
		return
	}
	sess := ctx.Session()
	if sess == nil {
		return
	}
	events := sess.Events()
	if events == nil {
		return
	}
	texts := make(map[string]bool)
	for event := range events.All() {
		if event == nil || event.Actions.Compaction == nil || event.Actions.Compaction.CompactedContent == nil {
			continue
		}
		for _, part := range event.Actions.Compaction.CompactedContent.Parts {
			if part != nil && part.Text != "" {
				texts[part.Text] = true
			}
		}
	}
	for i, turn := range c.turns {
		c.turns[i].pinned = turn.pinned ||
			slices.ContainsFunc(c.contents[turn.start:turn.end], func(content *genai.Content) bool {
				return content != nil && slices.ContainsFunc(content.Parts, func(part *genai.Part) bool {
					return part != nil && texts[part.Text]
				})
			})
	}
}

func (c *conversation) protectToolPairs() {
	pending := make(map[string]int)
	for i := range c.turns {
		for _, content := range c.contents[c.turns[i].start:c.turns[i].end] {
			if content == nil {
				continue
			}
			for _, part := range content.Parts {
				c.protectPart(part, i, pending)
			}
		}
	}
	for _, i := range pending {
		c.turns[i].pinned = true
	}
}

func (c *conversation) protectPart(part *genai.Part, i int, pending map[string]int) {
	if part == nil {
		return
	}
	if call := part.FunctionCall; call != nil {
		key := toolKey(call.ID, call.Name)
		if other, found := pending[key]; found {
			c.turns[other].pinned, c.turns[i].pinned = true, true
		}
		pending[key] = i
	}
	if response := part.FunctionResponse; response != nil {
		key := toolKey(response.ID, response.Name)
		other, found := pending[key]
		if !found || other != i {
			c.turns[i].pinned = true
			if found {
				c.turns[other].pinned = true
			}
		}
		delete(pending, key)
	}
}

func (c *conversation) selectTurns(decisions []Decision) ([]*genai.Content, int) {
	var selected []*genai.Content
	removed, start := 0, 0
	for _, decision := range decisions {
		if decision.Remove {
			selected = append(selected, c.contents[start:decision.Start]...)
			start = decision.End
			removed++
		}
	}
	if removed == 0 {
		return c.contents, 0
	}
	return append(selected, c.contents[start:]...), removed
}

func isConversationContent(content *genai.Content) bool {
	return content != nil && (content.Role == genai.RoleUser || content.Role == genai.RoleModel || content.Role == "")
}

func isUserMessage(content *genai.Content) bool {
	if content == nil || (content.Role != genai.RoleUser && content.Role != "") {
		return false
	}
	return !slices.ContainsFunc(content.Parts, func(part *genai.Part) bool {
		return part != nil && (part.FunctionCall != nil || part.FunctionResponse != nil)
	})
}

func toolKey(id, name string) string {
	if id != "" {
		return "id:" + id
	}
	return "name:" + name
}
