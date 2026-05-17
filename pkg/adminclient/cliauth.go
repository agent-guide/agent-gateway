package adminclient

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) ListCLIAuthAuthenticators(ctx context.Context) ([]CLIAuthAuthenticator, error) {
	var resp itemsResponse[CLIAuthAuthenticator]
	if err := c.do(ctx, http.MethodGet, "/admin/cliauth/authenticators", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetCLIAuthAuthenticator(ctx context.Context, name string) (*CLIAuthAuthenticator, error) {
	var resp CLIAuthAuthenticator
	if err := c.do(ctx, http.MethodGet, "/admin/cliauth/authenticators/"+url.PathEscape(name), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateCLIAuthAuthenticator(ctx context.Context, name string, req UpdateCLIAuthAuthenticatorRequest) (*CLIAuthUpdateAuthenticatorResponse, error) {
	var resp CLIAuthUpdateAuthenticatorResponse
	if err := c.do(ctx, http.MethodPut, "/admin/cliauth/authenticators/"+url.PathEscape(name), req, &resp, true, http.StatusOK, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) StartCLIAuthLogin(ctx context.Context, name string, req StartCLIAuthLoginRequest) (*CLIAuthLogin, error) {
	var resp CLIAuthLogin
	if err := c.do(ctx, http.MethodPost, "/admin/cliauth/authenticators/"+url.PathEscape(name)+"/login", req, &resp, true, http.StatusAccepted); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetCLIAuthLoginStatus(ctx context.Context, loginID string) (*CLIAuthLoginStatus, error) {
	var resp CLIAuthLoginStatus
	if err := c.do(ctx, http.MethodGet, "/admin/cliauth/logins/"+url.PathEscape(loginID), nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetCLIAuthRefresherStatus(ctx context.Context) (*CLIAuthRefresherStatus, error) {
	var resp CLIAuthRefresherStatus
	if err := c.do(ctx, http.MethodGet, "/admin/cliauth/refresher", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) EnableCLIAuthRefresher(ctx context.Context) (*boolStatusResponse, error) {
	var resp boolStatusResponse
	if err := c.do(ctx, http.MethodPost, "/admin/cliauth/refresher/enable", nil, &resp, true, http.StatusOK, http.StatusCreated); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DisableCLIAuthRefresher(ctx context.Context) (*boolStatusResponse, error) {
	var resp boolStatusResponse
	if err := c.do(ctx, http.MethodPost, "/admin/cliauth/refresher/disable", nil, &resp, true, http.StatusOK); err != nil {
		return nil, err
	}
	return &resp, nil
}
