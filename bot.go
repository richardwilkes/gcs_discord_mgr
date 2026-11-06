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
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/richardwilkes/toolbox/v2/errs"
	"github.com/richardwilkes/toolbox/v2/xio"
	"github.com/richardwilkes/toolbox/v2/xos"
)

const (
	cmdLink    = "link"
	cmdUnlink  = "unlink"
	cmdSponsor = "sponsor"
	subAudit   = "audit"
	subSync    = "sync"
	optMember  = "member"
	optGitHub  = "github"

	memberPageSize = 1000
	// linkTimeout is kept under the 15 minutes that Discord allows for following up on an interaction.
	linkTimeout = 14 * time.Minute

	msgTryLater   = "Something went wrong. Please try again later."
	msgRolesLater = " The sponsor roles could not be updated right now, but will be shortly."
)

type bot struct {
	session  *discordgo.Session
	cfg      *config
	gh       *gitHub
	links    *linkStore
	sponsors map[int64]sponsor // Most recent successful fetch; nil until there has been one
	linking  map[string]bool   // Members with a device flow in progress
	selfID   string            // The bot's user ID, which is also its application ID; set by checkRoles
	lock     sync.Mutex        // Guards sponsors and linking
	roleLock sync.Mutex        // Serializes link changes with the role changes that follow from them
	dryRun   bool
}

type syncStats struct {
	sponsors int
	members  int
	changes  int
	failures int
}

func newBot(cfg *config, gh *gitHub, links *linkStore, dryRun bool) (*bot, error) {
	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers
	session.StateEnabled = false
	return &bot{
		session: session,
		cfg:     cfg,
		gh:      gh,
		links:   links,
		linking: make(map[string]bool),
		dryRun:  dryRun,
	}, nil
}

func commands() []*discordgo.ApplicationCommand {
	manageRoles := int64(discordgo.PermissionManageRoles)
	memberOption := &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionUser,
		Name:        optMember,
		Description: "The Discord member",
		Required:    true,
	}
	return []*discordgo.ApplicationCommand{
		{
			Name:        cmdLink,
			Description: "Link your GitHub account to receive your sponsor roles",
		},
		{
			Name:        cmdUnlink,
			Description: "Remove the link to your GitHub account, along with your sponsor roles",
		},
		{
			Name:                     cmdSponsor,
			Description:              "Manage sponsor links",
			DefaultMemberPermissions: &manageRoles,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        cmdLink,
					Description: "Link a member to a GitHub user or organization",
					Options: []*discordgo.ApplicationCommandOption{
						memberOption,
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        optGitHub,
							Description: "The GitHub login",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        cmdUnlink,
					Description: "Remove a member's link, along with their sponsor roles",
					Options:     []*discordgo.ApplicationCommandOption{memberOption},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        subAudit,
					Description: "Report sponsor roles and links that need attention",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        subSync,
					Description: "Check GitHub and update sponsor roles now",
				},
			},
		},
	}
}

// listRoles prints the server's roles, highest first.
func (b *bot) listRoles() error {
	roles, err := b.session.GuildRoles(b.cfg.GuildID)
	if err != nil {
		return errs.NewWithCause("unable to list the server's roles", err)
	}
	slices.SortFunc(roles, func(a, c *discordgo.Role) int { return cmp.Compare(c.Position, a.Position) })
	for _, role := range roles {
		fmt.Printf("%-20s  %s\n", role.ID, role.Name)
	}
	return nil
}

// checkRoles verifies that the configured roles exist, and warns about any that the bot is not placed high enough to
// manage.
func (b *bot) checkRoles() error {
	roles, err := b.session.GuildRoles(b.cfg.GuildID)
	if err != nil {
		return errs.NewWithCause("unable to list the server's roles", err)
	}
	byID := make(map[string]*discordgo.Role, len(roles))
	for _, role := range roles {
		byID[role.ID] = role
	}
	managed := b.cfg.managedRoles()
	for _, id := range managed {
		if byID[id] == nil {
			return errs.Newf("the role ID %s configured for the %s role does not exist in the server", id,
				b.cfg.roleLabel(id))
		}
	}
	var self *discordgo.User
	if self, err = b.session.User("@me"); err != nil {
		return errs.NewWithCause("unable to identify the bot", err)
	}
	b.selfID = self.ID
	var member *discordgo.Member
	if member, err = b.session.GuildMember(b.cfg.GuildID, self.ID); err != nil {
		return errs.NewWithCause("unable to look up the bot's server membership", err)
	}
	top := 0
	for _, id := range member.Roles {
		if role := byID[id]; role != nil {
			top = max(top, role.Position)
		}
	}
	for _, id := range managed {
		if role := byID[id]; role.Position >= top {
			slog.Warn("the bot's role must be above this role in the server's role list to manage it", "role", role.Name)
		}
	}
	return nil
}

// start connects to Discord and registers the commands and event handlers.
func (b *bot) start() error {
	if err := b.checkRoles(); err != nil {
		return err
	}
	b.session.AddHandler(b.onMemberAdd)
	b.session.AddHandler(b.onInteraction)
	if err := b.session.Open(); err != nil {
		return errs.NewWithCause("unable to connect to Discord; is the Server Members Intent enabled for the bot?", err)
	}
	xos.RunAtExit(func() { xio.CloseIgnoringErrors(b.session) })
	if _, err := b.session.ApplicationCommandBulkOverwrite(b.selfID, b.cfg.GuildID, commands()); err != nil {
		return errs.NewWithCause("unable to register commands", err)
	}
	slog.Info("connected", "dry_run", b.dryRun)
	return nil
}

// run syncs repeatedly, forever.
func (b *bot) run() {
	for first := true; ; first = false {
		stats, err := b.sync()
		switch {
		case err != nil:
			errs.Log(errs.NewWithCause("sync skipped; no roles were changed", err))
		case first || stats.changes != 0 || stats.failures != 0:
			slog.Info("sync complete", stats.attrs()...)
		default:
			slog.Debug("sync complete", stats.attrs()...)
		}
		time.Sleep(b.cfg.PollInterval)
	}
}

func (s syncStats) attrs() []any {
	return []any{"sponsors", s.sponsors, "members", s.members, "changes", s.changes, "failures", s.failures}
}

// sync fetches the sponsors and brings every member's sponsor roles up to date. If the sponsors or the members cannot
// be fetched in full, nothing is changed.
func (b *bot) sync() (syncStats, error) {
	var stats syncStats
	sponsors, err := b.refreshSponsors()
	if err != nil {
		return stats, err
	}
	var members []*discordgo.Member
	if members, err = b.members(); err != nil {
		return stats, err
	}
	stats.sponsors = len(sponsors)
	stats.members = len(members)
	for _, m := range members {
		var changes int
		if _, changes, err = b.reconcile(m); err != nil {
			stats.failures++
			errs.Log(err)
		}
		stats.changes += changes
	}
	return stats, nil
}

func (b *bot) refreshSponsors() (map[int64]sponsor, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sponsors, err := b.gh.sponsors(ctx)
	if err != nil {
		return nil, err
	}
	b.lock.Lock()
	b.sponsors = sponsors
	b.lock.Unlock()
	return sponsors, nil
}

// knownSponsors returns the most recently fetched sponsors, fetching them if that has not succeeded yet.
func (b *bot) knownSponsors() (map[int64]sponsor, error) {
	b.lock.Lock()
	sponsors := b.sponsors
	b.lock.Unlock()
	if sponsors != nil {
		return sponsors, nil
	}
	return b.refreshSponsors()
}

func (b *bot) members() ([]*discordgo.Member, error) {
	var all []*discordgo.Member
	after := ""
	for {
		page, err := b.session.GuildMembers(b.cfg.GuildID, after, memberPageSize)
		if err != nil {
			return nil, errs.NewWithCause("unable to list the server's members", err)
		}
		for _, m := range page {
			if m.User != nil {
				all = append(all, m)
				after = m.User.ID
			}
		}
		if len(page) < memberPageSize {
			return all, nil
		}
	}
}

func (b *bot) member(discordID string) (*discordgo.Member, error) {
	m, err := b.session.GuildMember(b.cfg.GuildID, discordID)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	if m.User == nil {
		m.User = &discordgo.User{ID: discordID}
	}
	return m, nil
}

func isUnknownMember(err error) bool {
	var restErr *discordgo.RESTError
	return errors.As(err, &restErr) && restErr.Message != nil &&
		(restErr.Message.Code == discordgo.ErrCodeUnknownMember || restErr.Message.Code == discordgo.ErrCodeUnknownUser)
}

// reconcile brings a member's sponsor roles up to date, returning the roles their sponsorship entitles them to, if
// any, and the number of role changes made.
func (b *bot) reconcile(m *discordgo.Member) (roles []string, changes int, err error) {
	b.roleLock.Lock()
	defer b.roleLock.Unlock()
	return b.reconcileLocked(m)
}

// reconcileLocked is reconcile for callers that already hold roleLock.
func (b *bot) reconcileLocked(m *discordgo.Member) (roles []string, changes int, err error) {
	l, linked := b.links.get(m.User.ID)
	dollars := 0
	if linked {
		var sponsors map[int64]sponsor
		if sponsors, err = b.knownSponsors(); err != nil {
			return nil, 0, err
		}
		dollars = sponsors[l.GitHubID].Dollars
		roles = b.cfg.desiredRoles(dollars)
	}
	add, remove := b.cfg.planMember(m.Roles, linked, dollars)
	changes, err = b.changeRoles(m, add, remove)
	return roles, changes, err
}

// changeRoles adds before removing, so that a member changing tiers is never briefly without a tier role.
func (b *bot) changeRoles(m *discordgo.Member, add, remove []string) (changes int, err error) {
	for _, role := range add {
		if err = b.changeRole(m, role, true); err != nil {
			return changes, err
		}
		changes++
	}
	for _, role := range remove {
		if err = b.changeRole(m, role, false); err != nil {
			return changes, err
		}
		changes++
	}
	return changes, nil
}

func (b *bot) changeRole(m *discordgo.Member, role string, add bool) error {
	action := "remove"
	if add {
		action = "add"
	}
	attrs := []any{
		"action", action,
		"role", b.cfg.roleLabel(role),
		"member", m.User.Username,
		"member_id", m.User.ID,
	}
	if b.dryRun {
		slog.Info("dry run: role change skipped", attrs...)
		return nil
	}
	var err error
	if add {
		err = b.session.GuildMemberRoleAdd(b.cfg.GuildID, m.User.ID, role)
	} else {
		err = b.session.GuildMemberRoleRemove(b.cfg.GuildID, m.User.ID, role)
	}
	if err != nil {
		return errs.NewWithCausef(err, "unable to %s the %s role for member %s", action, b.cfg.roleLabel(role),
			m.User.ID)
	}
	slog.Info("role changed", attrs...)
	return nil
}

// link records that a member owns a GitHub account and updates their roles to match, returning a message describing
// the outcome. by is the Discord ID of whoever made the link.
func (b *bot) link(discordID string, acct *account, by string) string {
	b.roleLock.Lock()
	defer b.roleLock.Unlock()
	displaced, err := b.links.set(discordID, link{
		LinkedAt:    time.Now().UTC(),
		GitHubLogin: acct.Login,
		LinkedBy:    by,
		GitHubID:    acct.ID,
	})
	if err != nil {
		errs.Log(err)
		return "The link could not be saved. Please try again later."
	}
	slog.Info("linked", "member_id", discordID, "github", acct.Login, "github_id", acct.ID, "by", by)
	if displaced != "" {
		slog.Info("link displaced", "member_id", displaced, "github", acct.Login)
		b.stripLocked(displaced)
	}
	// Pick up a sponsorship that started after the last poll. On failure, the previous fetch is used.
	if _, err = b.refreshSponsors(); err != nil {
		errs.Log(err)
	}
	msg := fmt.Sprintf("<@%s> is now linked to the GitHub account **%s**.", discordID, acct.Login)
	var m *discordgo.Member
	if m, err = b.member(discordID); err != nil {
		if isUnknownMember(err) {
			return msg + " They are not in this server; their sponsor roles will be assigned when they join."
		}
		errs.Log(err)
		return msg + msgRolesLater
	}
	var roles []string
	if roles, _, err = b.reconcileLocked(m); err != nil {
		errs.Log(err)
		return msg + msgRolesLater
	}
	if len(roles) == 0 {
		return msg + fmt.Sprintf(" No active monthly sponsorship was found for that account. A new sponsorship is "+
			"picked up within %v.", b.cfg.PollInterval)
	}
	mentions := make([]string, len(roles))
	for j, role := range roles {
		mentions[j] = "<@&" + role + ">"
	}
	msg += " Sponsor roles: " + strings.Join(mentions, ", ") + "."
	if b.dryRun {
		msg += " (Dry run: no roles were changed.)"
	}
	return msg
}

// unlink removes a member's link and their sponsor roles, returning false if they had no link.
func (b *bot) unlink(discordID string) (bool, error) {
	b.roleLock.Lock()
	defer b.roleLock.Unlock()
	removed, err := b.links.remove(discordID)
	if err != nil || !removed {
		return false, err
	}
	slog.Info("unlinked", "member_id", discordID)
	b.stripLocked(discordID)
	return true, nil
}

// stripLocked removes the sponsor roles of a member whose link has just been removed. roleLock must be held.
func (b *bot) stripLocked(discordID string) {
	m, err := b.member(discordID)
	if err != nil {
		if !isUnknownMember(err) {
			errs.Log(err)
		}
		return
	}
	_, remove := b.cfg.planMember(m.Roles, true, 0)
	if _, err = b.changeRoles(m, nil, remove); err != nil {
		errs.Log(err)
	}
}

func (b *bot) onMemberAdd(_ *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e.Member == nil || e.User == nil || e.GuildID != b.cfg.GuildID {
		return
	}
	if _, _, err := b.reconcile(e.Member); err != nil {
		errs.Log(err)
	}
}

func (b *bot) onInteraction(_ *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand || i.GuildID != b.cfg.GuildID || i.Member == nil ||
		i.Member.User == nil {
		return
	}
	if err := b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		errs.Log(errs.Wrap(err))
		return
	}
	data := i.ApplicationCommandData()
	switch data.Name {
	case cmdLink:
		b.selfLink(i)
	case cmdUnlink:
		b.selfUnlink(i)
	case cmdSponsor:
		// The command's default permission can be overridden in the server's settings, so check here too.
		if i.Member.Permissions&(discordgo.PermissionManageRoles|discordgo.PermissionAdministrator) == 0 {
			b.reply(i, "You need the Manage Roles permission to use this command.")
			return
		}
		if len(data.Options) == 0 {
			return
		}
		switch sub := data.Options[0]; sub.Name {
		case cmdLink:
			b.adminLink(i, sub)
		case cmdUnlink:
			b.adminUnlink(i, sub)
		case subAudit:
			b.adminAudit(i)
		case subSync:
			b.adminSync(i)
		}
	}
}

// reply fills in the deferred response to an interaction.
func (b *bot) reply(i *discordgo.InteractionCreate, content string, files ...*discordgo.File) {
	if _, err := b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:         &content,
		Files:           files,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		errs.Log(errs.Wrap(err))
	}
}

// followup sends a further private message for an interaction that has already been replied to.
func (b *bot) followup(i *discordgo.InteractionCreate, content string) {
	if _, err := b.session.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content:         content,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
		Flags:           discordgo.MessageFlagsEphemeral,
	}); err != nil {
		errs.Log(errs.Wrap(err))
	}
}

// beginLinking marks a member as having a device flow in progress, returning false if they already do.
func (b *bot) beginLinking(discordID string) bool {
	b.lock.Lock()
	defer b.lock.Unlock()
	if b.linking[discordID] {
		return false
	}
	b.linking[discordID] = true
	return true
}

func (b *bot) endLinking(discordID string) {
	b.lock.Lock()
	delete(b.linking, discordID)
	b.lock.Unlock()
}

func (b *bot) selfLink(i *discordgo.InteractionCreate) {
	discordID := i.Member.User.ID
	if !b.beginLinking(discordID) {
		b.reply(i, "You already have a link in progress. Enter the code you were given, or wait for it to expire.")
		return
	}
	defer b.endLinking(discordID)
	ctx, cancel := context.WithTimeout(context.Background(), linkTimeout)
	defer cancel()
	dc, err := b.gh.startDeviceFlow(ctx)
	if err != nil {
		errs.Log(err)
		b.reply(i, msgTryLater)
		return
	}
	b.reply(i, fmt.Sprintf("To link your GitHub account:\n1. Open <%s>\n2. Sign in and enter the code **%s**\n\n"+
		"The code expires in %d minutes. This only confirms which GitHub account is yours; it grants no access to it.",
		dc.VerificationURI, dc.UserCode, int(linkTimeout.Minutes())))
	var token string
	if token, err = b.gh.awaitDeviceToken(ctx, dc); err != nil {
		switch {
		case errors.Is(err, errDeviceCodeExpired):
			b.followup(i, "The code expired before it was used. Run `/link` again for a new one.")
		case errors.Is(err, errDeviceAccessDenied):
			b.followup(i, "The link was canceled on GitHub.")
		default:
			errs.Log(err)
			b.followup(i, msgTryLater)
		}
		return
	}
	// The interaction's deadline has nearly passed if the code was entered late, so don't reuse ctx here.
	lookupCtx, lookupCancel := context.WithTimeout(context.Background(), gitHubRequestTimeout)
	defer lookupCancel()
	var acct *account
	if acct, err = b.gh.tokenAccount(lookupCtx, token); err != nil {
		errs.Log(err)
		b.followup(i, msgTryLater)
		return
	}
	b.followup(i, b.link(discordID, acct, discordID))
}

func (b *bot) selfUnlink(i *discordgo.InteractionCreate) {
	removed, err := b.unlink(i.Member.User.ID)
	switch {
	case err != nil:
		errs.Log(err)
		b.reply(i, msgTryLater)
	case removed:
		b.reply(i, "Your GitHub link and sponsor roles have been removed.")
	default:
		b.reply(i, "You don't have a linked GitHub account.")
	}
}

func (b *bot) adminLink(i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	memberOpt := sub.GetOption(optMember)
	gitHubOpt := sub.GetOption(optGitHub)
	if memberOpt == nil || gitHubOpt == nil {
		return
	}
	login := strings.TrimPrefix(strings.TrimSpace(gitHubOpt.StringValue()), "@")
	if login == "" {
		b.reply(i, "A GitHub login is required.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitHubRequestTimeout)
	defer cancel()
	acct, err := b.gh.lookupAccount(ctx, login)
	switch {
	case errors.Is(err, errNoSuchAccount):
		b.reply(i, "There is no GitHub account with that login.")
	case err != nil:
		errs.Log(err)
		b.reply(i, msgTryLater)
	default:
		b.reply(i, b.link(memberOpt.UserValue(nil).ID, acct, i.Member.User.ID))
	}
}

func (b *bot) adminUnlink(i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	memberOpt := sub.GetOption(optMember)
	if memberOpt == nil {
		return
	}
	discordID := memberOpt.UserValue(nil).ID
	removed, err := b.unlink(discordID)
	switch {
	case err != nil:
		errs.Log(err)
		b.reply(i, msgTryLater)
	case removed:
		b.reply(i, fmt.Sprintf("The GitHub link and sponsor roles of <@%s> have been removed.", discordID))
	default:
		b.reply(i, fmt.Sprintf("<@%s> doesn't have a linked GitHub account.", discordID))
	}
}

func (b *bot) adminSync(i *discordgo.InteractionCreate) {
	stats, err := b.sync()
	if err != nil {
		errs.Log(err)
		b.reply(i, "The sync could not be completed, so no roles were changed. See the log for details.")
		return
	}
	msg := fmt.Sprintf("Sync complete: %d active sponsors, %d members, %d role changes, %d failures.", stats.sponsors,
		stats.members, stats.changes, stats.failures)
	if b.dryRun {
		msg += " (Dry run: no roles were changed.)"
	}
	b.reply(i, msg)
}

func (b *bot) adminAudit(i *discordgo.InteractionCreate) {
	sponsors, err := b.refreshSponsors()
	if err != nil {
		errs.Log(err)
		b.reply(i, msgTryLater)
		return
	}
	var members []*discordgo.Member
	if members, err = b.members(); err != nil {
		errs.Log(err)
		b.reply(i, msgTryLater)
		return
	}
	summary, report := audit(b.cfg, b.links.all(), sponsors, members)
	b.reply(i, summary, &discordgo.File{
		Name:        "sponsor-audit.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader(report),
	})
}

// audit reports the members, links and sponsors that the bot does not act on but an admin may want to.
func audit(
	cfg *config, links map[string]link, sponsors map[int64]sponsor, members []*discordgo.Member,
) (summary, report string) {
	var unlinkedHolders, lapsed, departed, unlinkedSponsors []string
	present := make(map[string]bool, len(members))
	for _, m := range members {
		present[m.User.ID] = true
		who := fmt.Sprintf("%s (%s)", m.User.Username, m.User.ID)
		l, linked := links[m.User.ID]
		if linked {
			if sponsors[l.GitHubID].Dollars == 0 {
				lapsed = append(lapsed, who+": GitHub "+l.GitHubLogin)
			}
			continue
		}
		var held []string
		for _, role := range m.Roles {
			if label := cfg.roleLabel(role); label != "" {
				held = append(held, label)
			}
		}
		if len(held) != 0 {
			unlinkedHolders = append(unlinkedHolders, who+": "+strings.Join(held, ", "))
		}
	}
	linkedAccounts := make(map[int64]bool, len(links))
	for discordID, l := range links {
		linkedAccounts[l.GitHubID] = true
		if !present[discordID] {
			departed = append(departed, discordID+": GitHub "+l.GitHubLogin)
		}
	}
	privateSponsors := 0
	for id, s := range sponsors {
		switch {
		case linkedAccounts[id]:
		case s.Private:
			privateSponsors++
		default:
			unlinkedSponsors = append(unlinkedSponsors, fmt.Sprintf("%s: %s", s.Login, tierLabel(s.Dollars)))
		}
	}
	var buffer strings.Builder
	section := func(title string, lines []string) {
		slices.Sort(lines)
		fmt.Fprintf(&buffer, "%s (%d)\n", title, len(lines))
		for _, line := range lines {
			fmt.Fprintf(&buffer, "  %s\n", line)
		}
		buffer.WriteByte('\n')
	}
	section("Members holding a sponsor role without a link", unlinkedHolders)
	section("Linked members without an active sponsorship", lapsed)
	section("Links whose member has left the server", departed)
	section("Public sponsors not linked to a member", unlinkedSponsors)
	fmt.Fprintf(&buffer, "Private sponsors not linked to a member: %d\n", privateSponsors)
	summary = fmt.Sprintf("%d members hold a sponsor role without a link, %d linked members have no active sponsorship, "+
		"%d links are for members who have left, and %d sponsors (%d of them private) are not linked.",
		len(unlinkedHolders), len(lapsed), len(departed), len(unlinkedSponsors)+privateSponsors, privateSponsors)
	return summary, buffer.String()
}
