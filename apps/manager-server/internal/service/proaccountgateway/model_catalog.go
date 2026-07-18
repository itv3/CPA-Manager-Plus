package proaccountgateway

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// ListRuntimeModels 读取指定运行时凭证已经注册的模型列表。
func (c *Client) ListRuntimeModels(ctx context.Context, baseURL string, managementKey string, authIndex string, sourceLocator string) ([]string, error) {
	query := url.Values{}
	if value := strings.TrimSpace(authIndex); value != "" {
		query.Set("auth_index", value)
	} else if value := strings.TrimSpace(sourceLocator); value != "" {
		query.Set("name", value)
	}
	if len(query) == 0 {
		return nil, ErrGatewayAccountNotFound
	}
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/auth-files/models?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var payload struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Models))
	for _, item := range payload.Models {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, nil
}

// ListBuiltInModels 读取 Gateway 当前版本内置的模型目录。
func (c *Client) ListBuiltInModels(ctx context.Context, baseURL string, managementKey string, channel string) ([]string, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return []string{}, nil
	}
	raw, _, err := c.get(ctx, baseURL, managementKey, "/v0/management/model-definitions/"+url.PathEscape(channel))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Models))
	for _, item := range payload.Models {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, nil
}
