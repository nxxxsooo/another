package doubao

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nxxxsooo/another/internal/model"
)

type wireMessage struct {
	ID          string  `json:"message_id"`
	UserType    int     `json:"user_type"`
	Status      int     `json:"status"`
	Index       integer `json:"index_in_conv"`
	Created     integer `json:"create_time"`
	Content     string  `json:"content"`
	ContentType int     `json:"content_type"`
	Blocks      []struct {
		Type    int    `json:"block_type"`
		Parent  string `json:"parent_id"`
		Content struct {
			Text struct {
				Text string `json:"text"`
			} `json:"text_block"`
		} `json:"content"`
	} `json:"content_block"`
}

func (m wireMessage) text() string {
	var blocks []string
	for _, b := range m.Blocks {
		if b.Type == 10000 && b.Parent == "" && b.Content.Text.Text != "" {
			blocks = append(blocks, b.Content.Text.Text)
		}
	}
	if len(blocks) > 0 {
		return strings.Join(blocks, "\n")
	}
	var content struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(m.Content), &content) == nil {
		return content.Text
	}
	return ""
}

func (c *apiClient) messages(ctx context.Context, id string, limit int) ([]model.Message, error) {
	var all []wireMessage
	seen := map[string]bool{}
	var cursor integer
	for page := 0; page < 1000; page++ {
		var response struct {
			Messages []wireMessage `json:"messages"`
			More     bool          `json:"has_more"`
			Next     integer       `json:"next_index"`
		}
		direction := 1
		if cursor == 0 {
			direction = 3
		}
		err := c.im(ctx, "/im/chain/single", 3100, "pull_singe_chain_uplink_body", "pull_singe_chain_downlink_body", map[string]any{
			"conversation_id": id, "conversation_type": 3, "direction": direction, "anchor_index": cursor,
			"limit": 50, "evaluate_ab_params": "", "evaluate_common_params": "", "ext": map[string]string{},
		}, &response)
		if err != nil {
			return nil, err
		}
		for _, m := range response.Messages {
			if m.ID == "" {
				return nil, fmt.Errorf("doubao message has no stable identity")
			}
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			// Native invisible/deleted statuses must not become portable history.
			if m.Status == 1 || m.Status == 3 || m.Status == 5 || m.Status == 7 || m.Status == 19 {
				continue
			}
			if (m.UserType == 1 || m.UserType == 2) && m.text() != "" {
				all = append(all, m)
			}
		}
		if !response.More || (limit > 0 && len(all) >= limit) {
			sort.SliceStable(all, func(i, j int) bool { return all[i].Index < all[j].Index })
			if limit > 0 && len(all) > limit {
				all = all[len(all)-limit:]
			}
			result := make([]model.Message, 0, len(all))
			for _, m := range all {
				role := model.RoleUser
				if m.UserType == 2 {
					role = model.RoleAssistant
				}
				result = append(result, model.Message{Role: role, Content: m.text(), Timestamp: seconds(m.Created)})
			}
			return result, nil
		}
		if response.Next <= 0 || (cursor != 0 && response.Next >= cursor) {
			return nil, fmt.Errorf("doubao message pagination did not advance")
		}
		cursor = response.Next
	}
	return nil, fmt.Errorf("doubao message pagination exceeded limit")
}
