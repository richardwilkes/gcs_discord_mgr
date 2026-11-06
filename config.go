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
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/richardwilkes/toolbox/v2/errs"
	"gopkg.in/yaml.v3"
)

const (
	defaultPollInterval = 5 * time.Minute
	minPollInterval     = time.Minute
)

const sampleConfig = `# Bot token, from the Bot page of your application in the Discord Developer Portal.
discord_token: ""

# ID of the Discord server. With Developer Mode enabled, right-click the server and choose Copy Server ID.
guild_id: ""

# Classic GitHub personal access token for the sponsored account, with only the read:user scope.
github_token: ""

# Client ID of a GitHub OAuth app that has "Enable Device Flow" checked.
github_client_id: ""

# How often to check GitHub for sponsorship changes.
poll_interval: 5m

# ID of the Discord role that every active sponsor holds, whatever their tier. Run with -list-roles to see the
# server's role IDs.
sponsor_role: ""

# Monthly sponsorship amount in dollars -> ID of the Discord role for that tier.
tiers:
  1: ""
  2: ""
  5: ""
  10: ""
  25: ""
  50: ""
`

type config struct {
	Tiers          map[int]string `yaml:"tiers"`
	DiscordToken   string         `yaml:"discord_token"`
	GuildID        string         `yaml:"guild_id"`
	GitHubToken    string         `yaml:"github_token"`
	GitHubClientID string         `yaml:"github_client_id"`
	SponsorRole    string         `yaml:"sponsor_role"`
	PollInterval   time.Duration  `yaml:"poll_interval"`
}

// loadConfig reads the configuration file, writing a sample one in its place if it does not exist.
func loadConfig(path string) (*config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, errs.NewWithCause("unable to load configuration file", err)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, errs.NewWithCause("unable to create configuration directory", err)
		}
		if err = os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
			return nil, errs.NewWithCause("unable to write sample configuration file", err)
		}
		return nil, errs.New("The configuration file was missing. A sample was written to " + path +
			"; fill it in and run again.")
	}
	var cfg config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, errs.NewWithCause("unable to parse configuration file "+path, err)
	}
	cfg.DiscordToken = strings.TrimSpace(cfg.DiscordToken)
	cfg.GuildID = strings.TrimSpace(cfg.GuildID)
	cfg.GitHubToken = strings.TrimSpace(cfg.GitHubToken)
	cfg.GitHubClientID = strings.TrimSpace(cfg.GitHubClientID)
	cfg.SponsorRole = strings.TrimSpace(cfg.SponsorRole)
	for dollars, role := range cfg.Tiers {
		cfg.Tiers[dollars] = strings.TrimSpace(role)
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}
	cfg.PollInterval = max(cfg.PollInterval, minPollInterval)
	return &cfg, nil
}

// validate reports any settings that are missing or unusable. When discordOnly is true, only the settings needed to
// talk to Discord are checked.
func (c *config) validate(discordOnly bool) error {
	var problems []string
	if c.DiscordToken == "" {
		problems = append(problems, "discord_token is not set")
	}
	if c.GuildID == "" {
		problems = append(problems, "guild_id is not set")
	}
	if !discordOnly {
		if c.GitHubToken == "" {
			problems = append(problems, "github_token is not set")
		}
		if c.GitHubClientID == "" {
			problems = append(problems, "github_client_id is not set")
		}
		if c.SponsorRole == "" {
			problems = append(problems, "sponsor_role is not set")
		}
		problems = append(problems, c.tierProblems()...)
	}
	if len(problems) != 0 {
		return errs.New("Invalid configuration:\n  " + strings.Join(problems, "\n  "))
	}
	return nil
}

func (c *config) tierProblems() []string {
	if len(c.Tiers) == 0 {
		return []string{"tiers is empty"}
	}
	var problems []string
	seen := map[string]bool{c.SponsorRole: true}
	for _, dollars := range slices.Sorted(maps.Keys(c.Tiers)) {
		role := c.Tiers[dollars]
		switch {
		case dollars <= 0:
			problems = append(problems, "tiers has an amount that is not a positive number of dollars")
		case role == "":
			problems = append(problems, tierLabel(dollars)+" tier has no role ID")
		case seen[role]:
			problems = append(problems, "role ID "+role+" is used more than once")
		}
		seen[role] = true
	}
	return problems
}
