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
	"maps"
	"slices"
	"strconv"
)

const sponsorRoleLabel = "Sponsor"

func tierLabel(dollars int) string {
	return "$" + strconv.Itoa(dollars)
}

// managedRoles returns the IDs of the roles the bot assigns: the sponsor role, then the tier roles from lowest to
// highest.
func (c *config) managedRoles() []string {
	roles := make([]string, 0, len(c.Tiers)+1)
	roles = append(roles, c.SponsorRole)
	for _, dollars := range slices.Sorted(maps.Keys(c.Tiers)) {
		roles = append(roles, c.Tiers[dollars])
	}
	return roles
}

// roleLabel returns a short name for a managed role, or "" if the bot does not manage the role.
func (c *config) roleLabel(role string) string {
	if role == c.SponsorRole {
		return sponsorRoleLabel
	}
	for dollars, id := range c.Tiers {
		if id == role {
			return tierLabel(dollars)
		}
	}
	return ""
}

// desiredRoles returns the roles that someone sponsoring at dollars a month should hold: the sponsor role, plus the
// role of the highest tier that does not exceed dollars, if there is one. Zero dollars means not sponsoring.
func (c *config) desiredRoles(dollars int) []string {
	if dollars <= 0 {
		return nil
	}
	roles := []string{c.SponsorRole}
	best := 0
	for amount := range c.Tiers {
		if amount <= dollars && amount > best {
			best = amount
		}
	}
	if best != 0 {
		roles = append(roles, c.Tiers[best])
	}
	return roles
}

// planMember returns the managed roles to add to and remove from a member. Unlinked members are left alone; linked
// members end up holding exactly the roles for their monthly sponsorship. Unmanaged roles are never touched.
func (c *config) planMember(current []string, linked bool, dollars int) (add, remove []string) {
	if !linked {
		return nil, nil
	}
	desired := c.desiredRoles(dollars)
	for _, role := range desired {
		if !slices.Contains(current, role) {
			add = append(add, role)
		}
	}
	for _, role := range current {
		if c.roleLabel(role) != "" && !slices.Contains(desired, role) {
			remove = append(remove, role)
		}
	}
	return add, remove
}
