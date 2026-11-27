package siwc

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func modelsTransport(t *testing.T, handler roundTripFunc) (*http.Client, *[]string) {
	t.Helper()
	var authorizations []string
	client := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
		authorizations = append(authorizations, req.Header.Get("Authorization"))
		if req.Method != http.MethodGet {
			t.Errorf("models request method = %q, want GET", req.Method)
		}
		return handler(req)
	})
	return client, &authorizations
}

func TestListModelsFiltersListVisibilityInOrder(t *testing.T) {
	client, authorizations := modelsTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != ModelsPath {
			return jsonHTTPResponse(req, 404, "{}"), nil
		}
		body := `{"models":[
			{"slug":"gpt-a","display_name":"Model A","visibility":"list"},
			{"slug":"gpt-hidden","display_name":"Hidden","visibility":"hidden"},
			{"slug":"gpt-b","display_name":"Model B","visibility":"list"}
		]}`
		return textHTTPResponse(req, 200, body), nil
	})

	models, err := ListModels(t.Context(), testEndpoints, client, "token-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Slug != "gpt-a" || models[1].Slug != "gpt-b" {
		t.Fatalf("models = %+v", models)
	}
	if models[0].DisplayName != "Model A" {
		t.Fatalf("display name = %q", models[0].DisplayName)
	}
	if len(*authorizations) != 1 || (*authorizations)[0] != "Bearer token-1" {
		t.Fatalf("authorization headers = %v", *authorizations)
	}
}

func TestListModelsMapsAuthFailureToReauth(t *testing.T) {
	client, _ := modelsTransport(t, func(req *http.Request) (*http.Response, error) {
		return jsonHTTPResponse(req, http.StatusUnauthorized, `{"error":{"code":"subscription_sharing_invalid_user"}}`), nil
	})

	if _, err := ListModels(t.Context(), testEndpoints, client, "token-1"); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("ListModels() error = %v, want ErrReauthRequired", err)
	}
}

func TestListModelsRequiresArray(t *testing.T) {
	for _, body := range []string{`{}`, `{"models":null}`, `{"models":[]}`} {
		t.Run(body, func(t *testing.T) {
			client := stubHTTPClient(func(req *http.Request) (*http.Response, error) {
				resp := jsonHTTPResponse(req, http.StatusOK, body)
				resp.Header.Set("x-request-id", "models-request")
				return resp, nil
			})
			models, err := ListModels(t.Context(), testEndpoints, client, "token-1")
			if body == `{"models":[]}` {
				if err != nil || len(models) != 0 {
					t.Fatalf("empty array returned %v, %v", models, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "models-request") || !strings.Contains(err.Error(), "models 配列") {
				t.Fatalf("invalid response error = %v", err)
			}
		})
	}
}

func TestListModelsReportsStructuredErrorBody(t *testing.T) {
	client, _ := modelsTransport(t, func(req *http.Request) (*http.Response, error) {
		return textHTTPResponse(req, http.StatusForbidden, `{"error":{"code":"subscription_sharing_user_not_eligible","message":"user is not eligible"}}`), nil
	})

	_, err := ListModels(t.Context(), testEndpoints, client, "token-1")
	if err == nil || !strings.Contains(err.Error(), "subscription_sharing_user_not_eligible") {
		t.Fatalf("ListModels() error = %v, want the API error code", err)
	}
}

func textHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
