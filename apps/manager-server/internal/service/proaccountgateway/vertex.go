package proaccountgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

const maxVertexServiceAccount = 2 * 1024 * 1024

func (c *Client) ImportVertexDraft(ctx context.Context, baseURL string, managementKey string, input ImportVertexInput) (AccountSnapshot, error) {
	if len(input.ServiceAccount) == 0 || len(input.ServiceAccount) > maxVertexServiceAccount {
		return AccountSnapshot{}, errors.New("vertex service account size is invalid")
	}
	var serviceAccount map[string]any
	if json.Unmarshal(input.ServiceAccount, &serviceAccount) != nil {
		return AccountSnapshot{}, errors.New("vertex service account is invalid")
	}
	projectID, _ := serviceAccount["project_id"].(string)
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return AccountSnapshot{}, errors.New("vertex project id is required")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileName := filepath.Base(strings.TrimSpace(input.FileName))
	if fileName == "." || fileName == "" {
		fileName = "service-account.json"
	}
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		return AccountSnapshot{}, err
	}
	if _, err := part.Write(input.ServiceAccount); err != nil {
		return AccountSnapshot{}, err
	}
	if location := strings.TrimSpace(input.Location); location != "" {
		_ = writer.WriteField("location", location)
	}
	_ = writer.WriteField("credential_draft", "true")
	if err := writer.Close(); err != nil {
		return AccountSnapshot{}, err
	}
	raw, _, err := c.requestWithContentType(ctx, baseURL, managementKey, http.MethodPost, "/v0/management/vertex/import?credential_draft=true", &body, writer.FormDataContentType())
	if err != nil {
		return AccountSnapshot{}, err
	}
	var response struct {
		AuthFile string `json:"auth-file"`
		Project  string `json:"project_id"`
	}
	if json.Unmarshal(raw, &response) != nil || response.AuthFile == "" {
		return AccountSnapshot{}, errors.New("gateway vertex import returned an invalid response")
	}
	locator := filepath.Base(response.AuthFile)
	return c.waitForAccount(ctx, baseURL, managementKey, SourceAuthFile, locator, false)
}
