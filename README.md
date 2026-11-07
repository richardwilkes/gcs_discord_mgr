# GCS Discord Manager

A Discord bot that keeps the sponsor roles on the GCS Discord server in step with
[GitHub Sponsors](https://github.com/sponsors/richardwilkes) tiers.

GitHub does not know who a sponsor is on Discord, so each sponsor links their accounts once with the `/link` command.
From then on the bot assigns the Sponsor role and the role for their tier, changes the tier role when they change tier,
removes both when the sponsorship ends, and restores them if they leave the server and rejoin.

The bot only makes outbound connections. It needs no public address, open port, or webhook.

## How it behaves

- Every `poll_interval` it fetches the active monthly sponsorships from GitHub and checks every server member.
- A linked member with an active monthly sponsorship holds the Sponsor role plus exactly one tier role: the highest
  tier that their monthly amount covers. Custom amounts between tiers get the tier below. One-time payments are
  ignored.
- A linked member without an active sponsorship holds neither.
- Members who have not linked are never touched, even if they hold the Sponsor role or a tier role that was assigned
  by hand. `/sponsor audit` lists them.
- If GitHub or Discord cannot be reached, or returns an incomplete answer, that pass is skipped and no roles change.
- GitHub applies cancellations and downgrades at the sponsor's next billing date, so the role changes then.

## Commands

| Command                       | Who          | Does                                                              |
|-------------------------------|--------------|-------------------------------------------------------------------|
| `/link`                       | Everyone     | Links the member's GitHub account and assigns their sponsor roles |
| `/unlink`                     | Everyone     | Removes the member's link and sponsor roles                       |
| `/sponsor link member github` | Manage Roles | Links a member to a GitHub user or organization by login          |
| `/sponsor unlink member`      | Manage Roles | Removes a member's link and sponsor roles                         |
| `/sponsor list`               | Manage Roles | Lists the active sponsors with their Discord members and roles    |
| `/sponsor audit`              | Manage Roles | Reports roles, links and sponsors that need attention             |
| `/sponsor sync`               | Manage Roles | Checks GitHub and updates roles immediately                       |

All replies are visible only to the person who ran the command.

`/link` uses GitHub's device flow: the member is given a short code to enter at <https://github.com/login/device>. This
proves which GitHub account is theirs. No permissions are requested and the resulting token is discarded.

Linking the same GitHub account to a second Discord member moves the link, and the first member loses the roles.

The list and audit reports name public sponsors who have not linked, but only count private ones.

## Setup

### 1. Create the Discord application

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and sign in with the Discord
   account that owns the server.
2. Click **New Application** (shown as **Create App** in some versions of the portal), give it a name such as
   `GCS Sponsors`, accept the terms, and click **Create**. The name is what members will see as the bot's name.
3. In the left sidebar, open **Bot**.
   - Under **Token**, click **Reset Token**, confirm, and copy the token that appears. This is `discord_token` in the
     configuration file. It is shown only once; if you lose it, reset it again. Treat it like a password.
   - Under **Privileged Gateway Intents**, turn on **Server Members Intent**. Leave the other two off.
   - Click **Save Changes**.

### 2. Add the bot to the server

1. In the left sidebar, open **OAuth2** and scroll to **OAuth2 URL Generator**.
2. Under **Scopes**, check `bot` and `applications.commands`.
3. A **Bot Permissions** list appears below. Check **Manage Roles** and nothing else.
4. If an **Integration Type** choice is shown, leave it on **Guild Install**.
5. Copy the **Generated URL** at the bottom and open it in your browser.
6. Choose the GCS server under **Add to Server**, click **Continue**, then **Authorize**. You need the Manage Server
   permission on the server to do this.

The bot now appears in the server's member list, offline until the program is running.

### 3. Put the bot's role above the roles it manages

Discord only lets a bot assign roles that sit below its own role.

1. In Discord, open **Server Settings** → **Roles**.
2. Find the role with the bot's name; Discord created it when the bot was added.
3. Drag it above the Sponsor role and all of the tier roles, then click **Save Changes**.

### 4. Get the server ID

1. In Discord, open **User Settings** → **Advanced** and turn on **Developer Mode**.
2. Right-click the server's icon and choose **Copy Server ID**. This is `guild_id` in the configuration file.

### 5. Create the GitHub OAuth app

This is what sponsors see when they run `/link`, so give it a name they will recognize.

1. On GitHub, click your profile picture → **Settings** → **Developer settings** (bottom of the left sidebar) →
   **OAuth Apps** → **New OAuth App**.
2. Fill in **Application name**, and any URL of yours for both **Homepage URL** and **Authorization callback URL**.
   Both are required by the form, but the callback is never used.
3. Check **Enable Device Flow**, then click **Register application**.
4. Copy the **Client ID** shown on the next page. This is `github_client_id`. No client secret is needed.

### 6. Create the GitHub token

1. On GitHub, go to **Settings** → **Developer settings** → **Personal access tokens** → **Tokens (classic)** →
   **Generate new token** → **Generate new token (classic)**. This must be done as the account being sponsored.
2. Enter a note, choose an expiration, then under **user** check `read:user` and under **admin:org** check
   `read:org`. The `read:org` scope is needed because the sponsors query can return organization sponsors.
3. Click **Generate token** and copy it. This is `github_token`.

When the token expires the bot logs an error on every pass and stops changing roles until it is replaced.

### 7. Fill in the configuration

1. Run the bot once. It writes a sample configuration file and tells you where.
2. Fill in `discord_token`, `guild_id`, `github_token` and `github_client_id` from the steps above.
3. Run the bot with `-list-roles` to print the server's roles with their IDs, and copy the IDs into `sponsor_role` and
   `tiers`.
4. Run it with `-dry-run` and check the log before running it for real.

### Optional: stop others from adding the bot to their servers

The bot ignores every server but yours, so this is only tidiness. In the Developer Portal, open **Installation**, set
**Install Link** to **None** and save, then open **Bot** and turn off **Public Bot**. The order matters: Discord refuses
to make a bot private while it has an install link.

The configuration file defaults to `config.yaml` in the application data directory
(`~/Library/Application Support/com.trollworks.gcs-discord-mgr` on macOS,
`~/.local/share/com.trollworks.gcs-discord-mgr` on Linux). The links are stored beside it in `links.json`. Both hold
data worth protecting and are created readable only by their owner.

## Running

```
go build
./gcs_discord_mgr -list-roles   # Print role IDs, then exit
./gcs_discord_mgr -dry-run      # Log the role changes that would be made, without making them
./gcs_discord_mgr               # Run
```

Use `-h` to see the other options, including those for logging.

### launchd (macOS)

Save as `~/Library/LaunchAgents/com.trollworks.gcs-discord-mgr.plist`, adjust the path, then run
`launchctl load ~/Library/LaunchAgents/com.trollworks.gcs-discord-mgr.plist`.

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.trollworks.gcs-discord-mgr</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/local/bin/gcs_discord_mgr</string>
		<string>-quiet</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
```

### systemd (Linux)

Save as `/etc/systemd/system/gcs-discord-mgr.service`, adjust the user and path, then run
`systemctl enable --now gcs-discord-mgr`.

```ini
[Unit]
Description=GCS Discord Manager
After=network-online.target
Wants=network-online.target

[Service]
User=gcs
ExecStart=/usr/local/bin/gcs_discord_mgr -quiet
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### Checking status

macOS:

```
launchctl list | grep gcs-discord-mgr
```

This prints three columns: PID, last exit status, label. A number in the PID column means the bot is running; `-` means
it is not. The exit status is from the last time it stopped: `0` is a clean exit, a positive number is the exit code
and a negative number is the signal that killed it. No output at all means the agent is not loaded, so run the
`launchctl load` command above. For the full picture, including the run count and why it last exited:

```
launchctl print gui/$(id -u)/com.trollworks.gcs-discord-mgr
```

Linux:

```
systemctl status gcs-discord-mgr
```

Either one only tells you whether the process is up. For what the bot is actually doing, read the log. By default it
is written to `~/Library/Logs/com.trollworks.gcs-discord-mgr/gcs_discord_mgr.log` on macOS and
`~/.local/share/com.trollworks.gcs-discord-mgr/Logs/gcs_discord_mgr.log` on Linux, under the service's user; `-log-file`
changes the location. A healthy bot shows a `connected` line from its last start and no errors on recent passes:

```
tail -n 50 ~/Library/Logs/com.trollworks.gcs-discord-mgr/gcs_discord_mgr.log
```

## Updating

The bot keeps no state in memory that matters across a restart — the links are in `links.json` and everything else is
re-fetched — so updating is just replacing the binary and restarting. The only cost is that the bot is offline for a
moment and the next poll starts the interval over.

1. Get the new code and build it:

   ```
   git pull
   ./build.sh --all
   ```

   `--all` also runs the linters and the tests with race checking; stop here if anything fails.

2. If the service runs a copy of the binary rather than the build output itself, replace the copy:

   ```
   sudo cp gcs_discord_mgr /usr/local/bin/
   ```

   If the service points straight at the build output, the build already replaced the file, but the running process
   still has the old code loaded until it is restarted.

3. Restart the service. Both launchd (`KeepAlive`) and systemd (`Restart=always`) are configured to restart the
   program, so ask them to do it rather than killing the process by hand:

   macOS:

   ```
   launchctl kickstart -k gui/$(id -u)/com.trollworks.gcs-discord-mgr
   ```

   Linux:

   ```
   sudo systemctl restart gcs-discord-mgr
   ```

4. Confirm it came back, as described in [Checking status](#checking-status): the service shows a PID, the log shows
   a new `connected` line, and the bot appears online in the server's member list.

If the new version misbehaves, put the previous binary back (`git checkout <old-tag-or-commit>` and rebuild, or restore
the saved copy) and restart the service the same way.
