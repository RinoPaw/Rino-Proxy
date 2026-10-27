package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ReloadPayload hot-reloads Mihomo from an in-memory candidate, without
// overwriting the service's configured file. A 204 confirms API acceptance
// only: callers MUST verify readiness and arrange their own rollback.
func (c *Client) ReloadPayload(ctx context.Context, payload []byte) error {
	if len(payload) == 0 || len(payload) > 16<<20 {
		return errors.New("reload payload must contain 1 to 16 MiB of YAML")
	}
	body, err := json.Marshal(struct {
		Path    string `json:"path"`
		Payload string `json:"payload"`
	}{Path: "", Payload: string(payload)})
	if err != nil {
		return errors.New("cannot encode reload payload")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/configs", bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot construct reload request")
	}
	request.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		request.Header.Set("Authorization", "Bearer "+c.secret)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("mihomo reload transport failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		// The error body may contain credentials or YAML and must not be logged.
		return fmt.Errorf("mihomo reload returned HTTP %d", response.StatusCode)
	}
	return nil
}
