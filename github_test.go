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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/richardwilkes/toolbox/v2/check"
)

func TestSponsors(t *testing.T) {
	for _, tc := range []struct {
		pages   map[string]string // Response body by requested cursor; "" is the first page
		want    map[int64]sponsor
		name    string
		wantErr bool
	}{
		{
			name: "pages are merged and one-time payments are skipped",
			pages: map[string]string{
				"": `{"data":{"viewer":{"sponsorshipsAsMaintainer":{
					"pageInfo":{"hasNextPage":true,"endCursor":"c1"},
					"nodes":[
						{"isOneTimePayment":false,"privacyLevel":"PUBLIC",
							"sponsorEntity":{"databaseId":1,"login":"alice"},"tier":{"monthlyPriceInDollars":5}},
						{"isOneTimePayment":true,"privacyLevel":"PUBLIC",
							"sponsorEntity":{"databaseId":2,"login":"once"},"tier":{"monthlyPriceInDollars":50}}
					]}}}}`,
				"c1": `{"data":{"viewer":{"sponsorshipsAsMaintainer":{
					"pageInfo":{"hasNextPage":false,"endCursor":"c2"},
					"nodes":[
						{"isOneTimePayment":false,"privacyLevel":"PRIVATE",
							"sponsorEntity":{"databaseId":3,"login":"acme"},"tier":{"monthlyPriceInDollars":25}}
					]}}}}`,
			},
			want: map[int64]sponsor{
				1: {Login: "alice", Dollars: 5},
				3: {Login: "acme", Dollars: 25, Private: true},
			},
		},
		{
			name: "errors alongside data are a failure",
			pages: map[string]string{
				"": `{"errors":[{"message":"boom"}],"data":{"viewer":{"sponsorshipsAsMaintainer":{
					"pageInfo":{"hasNextPage":false},"nodes":[]}}}}`,
			},
			wantErr: true,
		},
		{
			name:    "a response without data is a failure",
			pages:   map[string]string{"": `{"data":null}`},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := check.New(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Variables struct {
						Cursor string `json:"cursor"`
					} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				body, ok := tc.pages[req.Variables.Cursor]
				if !ok {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			g := &gitHub{client: server.Client(), apiURL: server.URL}
			got, err := g.sponsors(t.Context())
			if tc.wantErr {
				c.HasError(err)
				return
			}
			c.NoError(err)
			c.Equal(tc.want, got)
		})
	}
}
