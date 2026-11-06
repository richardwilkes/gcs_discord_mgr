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
	"testing"

	"github.com/richardwilkes/toolbox/v2/check"
)

func TestPlanMember(t *testing.T) {
	cfg := &config{
		SponsorRole: "rs",
		Tiers:       map[int]string{2: "r2", 5: "r5", 10: "r10"},
	}
	for _, tc := range []struct {
		name    string
		current []string
		add     []string
		remove  []string
		dollars int
		linked  bool
	}{
		{
			name:    "unlinked member keeps hand-assigned roles",
			current: []string{"rs", "r5"},
		},
		{
			name:    "new sponsor gains the sponsor role and their tier role",
			linked:  true,
			dollars: 5,
			add:     []string{"rs", "r5"},
		},
		{
			name:    "amount between tiers gets the highest tier it covers",
			linked:  true,
			dollars: 7,
			current: []string{"rs"},
			add:     []string{"r5"},
		},
		{
			name:    "amount above the top tier gets the top tier",
			linked:  true,
			dollars: 100,
			current: []string{"rs"},
			add:     []string{"r10"},
		},
		{
			name:    "amount below the lowest tier gets only the sponsor role",
			linked:  true,
			dollars: 1,
			add:     []string{"rs"},
		},
		{
			name:    "tier change swaps the tier role and keeps the sponsor role",
			linked:  true,
			dollars: 10,
			current: []string{"rs", "r5"},
			add:     []string{"r10"},
			remove:  []string{"r5"},
		},
		{
			name:    "ended sponsorship loses the sponsor role and the tier role",
			linked:  true,
			current: []string{"rs", "r5"},
			remove:  []string{"rs", "r5"},
		},
		{
			name:    "correct roles are left as is",
			linked:  true,
			dollars: 5,
			current: []string{"rs", "r5"},
		},
		{
			name:    "extra tier roles are removed but other roles are kept",
			linked:  true,
			dollars: 2,
			current: []string{"other", "rs", "r2", "r5", "r10"},
			remove:  []string{"r5", "r10"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := check.New(t)
			add, remove := cfg.planMember(tc.current, tc.linked, tc.dollars)
			c.Equal(tc.add, add)
			c.Equal(tc.remove, remove)
		})
	}
}
