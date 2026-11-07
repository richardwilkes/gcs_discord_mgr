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

	"github.com/bwmarrin/discordgo"
	"github.com/richardwilkes/toolbox/v2/check"
)

func TestSponsorList(t *testing.T) {
	c := check.New(t)
	cfg := &config{SponsorRole: "rs", Tiers: map[int]string{5: "r5", 10: "r10"}}
	links := map[string]link{
		"d1": {GitHubID: 1, GitHubLogin: "alice"},
		"d3": {GitHubID: 3, GitHubLogin: "carol"},
		"d4": {GitHubID: 4, GitHubLogin: "dave"},
		"d5": {GitHubID: 5, GitHubLogin: "erin"},
	}
	sponsors := map[int64]sponsor{
		1: {Login: "alice", Dollars: 50},
		2: {Login: "zed", Dollars: 25},
		3: {Login: "carol", Dollars: 7, Private: true},
		4: {Login: "dave", Dollars: 5},
		5: {Login: "erin", Dollars: 1},
		6: {Login: "hidden", Dollars: 10, Private: true},
		7: {Login: "acme", Dollars: 25},
	}
	members := []*discordgo.Member{
		{User: &discordgo.User{ID: "d1", Username: "alice"}, Roles: []string{"r10", "other", "rs"}},
		{User: &discordgo.User{ID: "d3", Username: "carol"}, Roles: []string{"rs", "r5"}},
		{User: &discordgo.User{ID: "d5", Username: "erin"}},
		{User: &discordgo.User{ID: "d9", Username: "nobody"}, Roles: []string{"rs"}},
	}
	summary, report := sponsorList(cfg, links, sponsors, members)
	c.Equal("7 active sponsors: 4 linked to a member, 3 not linked (1 of them private).", summary)
	c.Equal(`Active monthly sponsors (6)
  GitHub  Monthly  Discord member          Roles
  alice   $50      alice (d1)              Sponsor, $10
  acme    $25      not linked
  zed     $25      not linked
  carol   $7       carol (d3)              Sponsor, $5
  dave    $5       d4 (not in the server)
  erin    $1       erin (d5)               none

Private sponsors not linked to a member (not listed): 1
`, report)
}
