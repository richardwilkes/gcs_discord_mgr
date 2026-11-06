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
	"flag"
	"path/filepath"

	"github.com/richardwilkes/toolbox/v2/errs"
	"github.com/richardwilkes/toolbox/v2/xflag"
	"github.com/richardwilkes/toolbox/v2/xos"
	"github.com/richardwilkes/toolbox/v2/xslog"
)

func main() {
	xos.AppName = "GCS Discord Manager"
	xos.AppIdentifier = "com.trollworks.gcs-discord-mgr"
	xos.AppVersion = "1.0.0"
	xos.CopyrightHolder = "Richard A. Wilkes"
	xos.CopyrightStartYear = "2026"
	xos.License = "Mozilla Public License Version 2.0"
	configPath := flag.String("config", filepath.Join(xos.AppDataDir(true), "config.yaml"),
		"The configuration `file` to use")
	dryRun := flag.Bool("dry-run", false, "Log the role changes that would be made without making them")
	listRoles := flag.Bool("list-roles", false, "List the Discord server's roles and their IDs, then exit")
	xflag.AddVersionFlags()
	logCfg := xslog.Config{Console: true}
	logCfg.AddFlags()
	xflag.SetUsage(nil, "Keeps the sponsor roles on a Discord server in step with GitHub Sponsors tiers.", "")
	xflag.Parse()

	cfg, err := loadConfig(*configPath)
	if err == nil {
		err = cfg.validate(*listRoles)
	}
	if err != nil {
		xos.ExitWithMsg(errs.WrapTyped(err).Message())
	}
	var links *linkStore
	links, err = loadLinks(filepath.Join(filepath.Dir(*configPath), "links.json"))
	xos.ExitIfErr(err)
	var b *bot
	b, err = newBot(cfg, newGitHub(cfg.GitHubToken, cfg.GitHubClientID), links, *dryRun)
	xos.ExitIfErr(err)
	if *listRoles {
		xos.ExitIfErr(b.listRoles())
		xos.Exit(0)
	}
	xos.ExitIfErr(b.start())
	b.run()
}
