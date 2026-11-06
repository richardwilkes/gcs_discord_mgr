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
	"errors"
	"io"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/richardwilkes/toolbox/v2/errs"
	"github.com/richardwilkes/toolbox/v2/xos"
)

type link struct {
	LinkedAt    time.Time `json:"linked_at"`
	GitHubLogin string    `json:"github_login"`
	LinkedBy    string    `json:"linked_by"` // Discord ID of whoever created the link
	GitHubID    int64     `json:"github_id"`
}

// linkStore holds the Discord member ID to GitHub account links. A GitHub account is linked to at most one member.
// Changes are only kept in memory once they have been written to disk.
type linkStore struct {
	byDiscord map[string]link
	path      string
	lock      sync.RWMutex
}

func loadLinks(path string) (*linkStore, error) {
	s := &linkStore{
		byDiscord: make(map[string]link),
		path:      path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, errs.NewWithCause("unable to load links file", err)
	}
	if err = json.Unmarshal(data, &s.byDiscord); err != nil {
		return nil, errs.NewWithCause("unable to parse links file", err)
	}
	return s, nil
}

func (s *linkStore) get(discordID string) (link, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	l, ok := s.byDiscord[discordID]
	return l, ok
}

func (s *linkStore) all() map[string]link {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return maps.Clone(s.byDiscord)
}

// set links a member to a GitHub account, replacing any link either of them already had. It returns the ID of the
// member that previously held the GitHub account, if that was someone else.
func (s *linkStore) set(discordID string, l link) (displaced string, err error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	updated := maps.Clone(s.byDiscord)
	for id, existing := range updated {
		if existing.GitHubID == l.GitHubID && id != discordID {
			displaced = id
			delete(updated, id)
			break
		}
	}
	updated[discordID] = l
	if err = s.save(updated); err != nil {
		return "", err
	}
	return displaced, nil
}

// remove deletes a member's link, returning false if they had none.
func (s *linkStore) remove(discordID string) (bool, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, ok := s.byDiscord[discordID]; !ok {
		return false, nil
	}
	updated := maps.Clone(s.byDiscord)
	delete(updated, discordID)
	if err := s.save(updated); err != nil {
		return false, err
	}
	return true, nil
}

// save writes links to disk and, on success, makes them the current set. The write lock must be held.
func (s *linkStore) save(links map[string]link) error {
	data, err := json.MarshalIndent(links, "", "  ")
	if err != nil {
		return errs.Wrap(err)
	}
	if err = xos.WriteSafeFile(s.path, func(w io.Writer) error {
		_, writeErr := w.Write(data)
		return writeErr
	}); err != nil {
		return errs.NewWithCause("unable to save links file", err)
	}
	s.byDiscord = links
	return nil
}
