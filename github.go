// Copyright (c) 2026 by Richard A. Wilkes. All rights reserved.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, version 2.0. If a copy of the MPL was not distributed with
// this file, You can obtain one at http://mozilla.org/MPL/2.0/.
//
// This Source Code Form is "Incompatible With Secondary Licenses", as
// defined by the Mozilla Public License, version 2.0.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/richardwilkes/toolbox/v2/errs"
	"github.com/richardwilkes/toolbox/v2/xio"
)

const (
	gitHubAPIURL          = "https://api.github.com"
	gitHubWebURL          = "https://github.com"
	gitHubRequestTimeout  = 30 * time.Second
	gitHubMaxResponseSize = 4 << 20
	jsonContentType       = "application/json"
	formContentType       = "application/x-www-form-urlencoded"
	deviceGrantType       = "urn:ietf:params:oauth:grant-type:device_code"
	defaultDeviceInterval = 5 * time.Second
	slowDownIncrement     = 5 * time.Second
)

const sponsorsQuery = `query($cursor: String) {
  viewer {
    sponsorshipsAsMaintainer(first: 100, after: $cursor, activeOnly: true, includePrivate: true) {
      pageInfo { hasNextPage endCursor }
      nodes {
        isOneTimePayment
        privacyLevel
        sponsorEntity {
          ... on User { databaseId login }
          ... on Organization { databaseId login }
        }
        tier { monthlyPriceInDollars }
      }
    }
  }
}`

var (
	errDeviceCodeExpired  = errors.New("the device code expired")
	errDeviceAccessDenied = errors.New("the device authorization was denied")
	errNoSuchAccount      = errors.New("no such GitHub account")
)

type gitHub struct {
	client   *http.Client
	apiURL   string
	webURL   string
	token    string
	clientID string
}

type account struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

type sponsor struct {
	Login   string
	Dollars int
	Private bool
}

type deviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Error           string `json:"error"`
	Interval        int    `json:"interval"`
}

type sponsorsResponse struct {
	Data *struct {
		Viewer *struct {
			Sponsorships *struct {
				PageInfo struct {
					EndCursor   string `json:"endCursor"`
					HasNextPage bool   `json:"hasNextPage"`
				} `json:"pageInfo"`
				Nodes []struct {
					SponsorEntity *struct {
						Login      string `json:"login"`
						DatabaseID int64  `json:"databaseId"`
					} `json:"sponsorEntity"`
					Tier *struct {
						MonthlyPriceInDollars int `json:"monthlyPriceInDollars"`
					} `json:"tier"`
					PrivacyLevel     string `json:"privacyLevel"`
					IsOneTimePayment bool   `json:"isOneTimePayment"`
				} `json:"nodes"`
			} `json:"sponsorshipsAsMaintainer"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func newGitHub(token, clientID string) *gitHub {
	return &gitHub{
		client:   &http.Client{},
		apiURL:   gitHubAPIURL,
		webURL:   gitHubWebURL,
		token:    token,
		clientID: clientID,
	}
}

// sponsors returns the active monthly sponsorships of the token's owner, keyed by the sponsor's GitHub account ID.
// Anything short of a complete, error-free response is an error, so that a failure is never read as a loss of
// sponsors.
func (g *gitHub) sponsors(ctx context.Context) (map[int64]sponsor, error) {
	result := make(map[int64]sponsor)
	var cursor *string
	for {
		body, err := json.Marshal(map[string]any{
			"query":     sponsorsQuery,
			"variables": map[string]any{"cursor": cursor},
		})
		if err != nil {
			return nil, errs.Wrap(err)
		}
		var status int
		var data []byte
		if status, data, err = g.call(ctx, http.MethodPost, g.apiURL+"/graphql", g.token, jsonContentType,
			body); err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, responseErr("sponsors query", status, data)
		}
		var resp sponsorsResponse
		if err = json.Unmarshal(data, &resp); err != nil {
			return nil, errs.NewWithCause("unable to parse GitHub sponsors response", err)
		}
		if len(resp.Errors) != 0 {
			return nil, errs.New("GitHub sponsors query failed: " + resp.Errors[0].Message)
		}
		if resp.Data == nil || resp.Data.Viewer == nil || resp.Data.Viewer.Sponsorships == nil {
			return nil, errs.New("GitHub sponsors query returned no data")
		}
		page := resp.Data.Viewer.Sponsorships
		for _, node := range page.Nodes {
			if node.IsOneTimePayment || node.SponsorEntity == nil || node.SponsorEntity.DatabaseID == 0 ||
				node.Tier == nil {
				continue
			}
			id := node.SponsorEntity.DatabaseID
			if existing, ok := result[id]; ok && existing.Dollars >= node.Tier.MonthlyPriceInDollars {
				continue
			}
			result[id] = sponsor{
				Login:   node.SponsorEntity.Login,
				Dollars: node.Tier.MonthlyPriceInDollars,
				Private: node.PrivacyLevel == "PRIVATE",
			}
		}
		if !page.PageInfo.HasNextPage {
			return result, nil
		}
		next := page.PageInfo.EndCursor
		if next == "" || (cursor != nil && *cursor == next) {
			return nil, errs.New("GitHub sponsors query returned an unusable page cursor")
		}
		cursor = &next
	}
}

// startDeviceFlow asks GitHub for a code the user can enter at the verification URI to identify themselves.
func (g *gitHub) startDeviceFlow(ctx context.Context) (*deviceCode, error) {
	form := url.Values{"client_id": {g.clientID}}
	status, data, err := g.call(ctx, http.MethodPost, g.webURL+"/login/device/code", "", formContentType,
		[]byte(form.Encode()))
	if err != nil {
		return nil, err
	}
	var dc deviceCode
	if err = json.Unmarshal(data, &dc); err != nil || dc.Error != "" || dc.DeviceCode == "" || dc.UserCode == "" {
		return nil, responseErr("device code request", status, data)
	}
	if dc.VerificationURI == "" {
		dc.VerificationURI = g.webURL + "/login/device"
	}
	return &dc, nil
}

// awaitDeviceToken polls until the user enters the device code, returning an access token for them. It returns
// errDeviceCodeExpired if the code expires or ctx ends first, and errDeviceAccessDenied if the user declines.
func (g *gitHub) awaitDeviceToken(ctx context.Context, dc *deviceCode) (string, error) {
	interval := defaultDeviceInterval
	if dc.Interval > 0 {
		interval = time.Duration(dc.Interval) * time.Second
	}
	form := []byte(url.Values{
		"client_id":   {g.clientID},
		"device_code": {dc.DeviceCode},
		"grant_type":  {deviceGrantType},
	}.Encode())
	for {
		select {
		case <-ctx.Done():
			return "", errDeviceCodeExpired
		case <-time.After(interval):
		}
		status, data, err := g.call(ctx, http.MethodPost, g.webURL+"/login/oauth/access_token", "", formContentType,
			form)
		if err != nil {
			if ctx.Err() != nil {
				return "", errDeviceCodeExpired
			}
			slog.Warn("device token poll failed; will retry", "error", err)
			continue
		}
		var resp struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			Interval    int    `json:"interval"`
		}
		// The body is left out of these errors, since it may hold an access token.
		if err = json.Unmarshal(data, &resp); err != nil {
			return "", errs.Newf("GitHub device token request returned an unreadable response: status %d", status)
		}
		switch resp.Error {
		case "":
			if resp.AccessToken == "" {
				return "", errs.Newf("GitHub device token request returned no token: status %d", status)
			}
			return resp.AccessToken, nil
		case "authorization_pending":
		case "slow_down":
			if resp.Interval > 0 {
				interval = time.Duration(resp.Interval) * time.Second
			} else {
				interval += slowDownIncrement
			}
		case "expired_token":
			return "", errDeviceCodeExpired
		case "access_denied":
			return "", errDeviceAccessDenied
		default:
			return "", errs.New("GitHub device authorization failed: " + resp.Error)
		}
	}
}

// tokenAccount returns the account that a user's access token belongs to.
func (g *gitHub) tokenAccount(ctx context.Context, userToken string) (*account, error) {
	return g.account(ctx, "/user", userToken)
}

// lookupAccount returns the user or organization account with the given login, or errNoSuchAccount.
func (g *gitHub) lookupAccount(ctx context.Context, login string) (*account, error) {
	return g.account(ctx, "/users/"+url.PathEscape(login), g.token)
}

func (g *gitHub) account(ctx context.Context, path, token string) (*account, error) {
	status, data, err := g.call(ctx, http.MethodGet, g.apiURL+path, token, "", nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errNoSuchAccount
	}
	if status != http.StatusOK {
		return nil, responseErr("account lookup", status, data)
	}
	var acct account
	if err = json.Unmarshal(data, &acct); err != nil {
		return nil, errs.NewWithCause("unable to parse GitHub account response", err)
	}
	if acct.ID == 0 || acct.Login == "" {
		return nil, errs.New("GitHub account lookup returned no account")
	}
	return &acct, nil
}

// call sends a request and returns the status code and body of the response.
func (g *gitHub) call(
	ctx context.Context, method, target, token, contentType string, body []byte,
) (status int, data []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, gitHubRequestTimeout)
	defer cancel()
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body)); err != nil {
		return 0, nil, errs.Wrap(err)
	}
	req.Header.Set("Accept", jsonContentType)
	req.Header.Set("User-Agent", "gcs_discord_mgr")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	var resp *http.Response
	if resp, err = g.client.Do(req); err != nil {
		return 0, nil, errs.Wrap(err)
	}
	defer xio.CloseIgnoringErrors(resp.Body)
	if data, err = io.ReadAll(io.LimitReader(resp.Body, gitHubMaxResponseSize)); err != nil {
		return 0, nil, errs.Wrap(err)
	}
	return resp.StatusCode, data, nil
}

func responseErr(what string, status int, body []byte) error {
	const maxSnippet = 300
	snippet := string(body)
	if len(snippet) > maxSnippet {
		snippet = snippet[:maxSnippet] + "…"
	}
	return errs.Newf("GitHub %s failed: status %d: %s", what, status, snippet)
}
