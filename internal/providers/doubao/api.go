package doubao

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

const apiBase = "https://www.doubao.com"

type apiClient struct {
	http    *http.Client
	base    string
	headers http.Header
}

// integer accepts the decimal strings used by Doubao's IM gateway without
// passing 17-digit identities/cursors through float64.
type integer int64

func (n *integer) UnmarshalJSON(b []byte) error {
	var text string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &text); err != nil {
			return err
		}
	} else {
		text = string(b)
	}
	if text == "" || text == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(text, 10, 64)
	*n = integer(v)
	return err
}

func (c *apiClient) post(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header = c.headers.Clone()
	req.Header.Set("Content-Type", "application/json; encoding=utf-8")
	req.Header.Set("Agw-Js-Conv", "str")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("doubao request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("doubao HTTP %d; check desktop login and connection", resp.StatusCode)
	}
	const maxResponse = 32 << 20
	b, err = io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return err
	}
	if len(b) > maxResponse {
		return fmt.Errorf("doubao response exceeds 32 MiB")
	}
	if err := json.Unmarshal(b, out); err != nil {
		// Do not echo a response body: it can include account data or cookies.
		return fmt.Errorf("invalid Doubao response; check desktop login")
	}
	return nil
}

func (c *apiClient) im(ctx context.Context, path string, cmd int, up, down string, body, out any) error {
	var response struct {
		Status *int                       `json:"status_code"`
		Body   map[string]json.RawMessage `json:"downlink_body"`
	}
	err := c.post(ctx, path+"?aid=497858&device_platform=web", map[string]any{
		"cmd": cmd, "channel": 2, "version": "1", "sequence_id": uuid.NewString(),
		"uplink_body": map[string]any{up: body},
	}, &response)
	if err != nil {
		return err
	}
	if response.Status == nil {
		return fmt.Errorf("doubao IM authentication failed; sign in to the desktop app")
	}
	if *response.Status != 0 {
		return fmt.Errorf("doubao IM error %d", *response.Status)
	}
	data, ok := response.Body[down]
	if !ok || string(data) == "null" {
		return fmt.Errorf("doubao IM response missing %s", down)
	}
	return json.Unmarshal(data, out)
}

type conversation struct {
	ID      string  `json:"conversation_id"`
	Name    string  `json:"name"`
	Type    int     `json:"conversation_type"`
	Updated integer `json:"update_time"`
	Deleted bool    `json:"deleted"`
	Thread  string  `json:"-"`
}

func (c *apiClient) list(ctx context.Context) ([]conversation, error) {
	var result []conversation
	seen := map[string]bool{}
	var index integer
	for page := 0; page < 1000; page++ {
		var response struct {
			Code *int `json:"code"`
			Data *struct {
				Threads []struct {
					Thread       string       `json:"thread_id_str"`
					Conversation conversation `json:"conversation"`
				} `json:"thread_list"`
				More bool    `json:"has_more"`
				Next integer `json:"next_index"`
			} `json:"data"`
		}
		if err := c.post(ctx, "/samantha/conversation/list", map[string]any{"index": index, "count": 50}, &response); err != nil {
			return nil, err
		}
		if response.Code == nil || *response.Code != 0 || response.Data == nil {
			return nil, fmt.Errorf("doubao conversation listing failed; check desktop login")
		}
		for _, row := range response.Data.Threads {
			conv := row.Conversation
			if !validID(conv.ID) || seen[conv.ID] {
				continue
			}
			seen[conv.ID] = true
			conv.Thread = row.Thread
			if conv.Thread == "" {
				conv.Thread = conv.ID
			}
			result = append(result, conv)
		}
		if !response.Data.More {
			return result, nil
		}
		if response.Data.Next <= index {
			return nil, fmt.Errorf("doubao conversation pagination did not advance")
		}
		index = response.Data.Next
	}
	return nil, fmt.Errorf("doubao conversation pagination exceeded limit")
}

type metadata struct {
	ID      string  `json:"conversation_id"`
	Created integer `json:"create_time"`
	Version integer `json:"conv_version"`
	Status  int     `json:"conversation_status"`
}

func (c *apiClient) metadata(ctx context.Context) (map[string]metadata, error) {
	result := map[string]metadata{}
	var cursor integer
	for page := 0; page < 1000; page++ {
		var response struct {
			Cells []struct {
				Conversation metadata `json:"conversation"`
			} `json:"cells"`
			More bool    `json:"has_more"`
			Next integer `json:"next_conv_version"`
		}
		direction := 1
		if cursor == 0 {
			direction = 3
		}
		err := c.im(ctx, "/im/chain/recent_conv", 3200, "pull_recent_conv_chain_uplink_body", "pull_recent_conv_chain_downlink_body", map[string]any{
			"api_version": 1, "conv_version": cursor, "direction": direction, "limit": 50,
			"option": map[string]any{"not_need_message": true, "need_complete_conversation": true, "need_coco_conversation": cursor == 0, "need_coco_bot": cursor == 0},
		}, &response)
		if err != nil {
			return nil, err
		}
		for _, cell := range response.Cells {
			m := cell.Conversation
			if validID(m.ID) {
				if _, exists := result[m.ID]; !exists {
					result[m.ID] = m
				}
			}
		}
		if !response.More {
			return result, nil
		}
		if response.Next <= 0 || (cursor != 0 && response.Next >= cursor) {
			return nil, fmt.Errorf("doubao metadata pagination did not advance")
		}
		cursor = response.Next
	}
	return nil, fmt.Errorf("doubao metadata pagination exceeded limit")
}
