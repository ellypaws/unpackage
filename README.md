<p align="center">
  <h1 align="center">unpackage</h1>
</p>

<p align="center">
  Find messages that appear in an older Discord data package but not in a newer one.
  Everything runs locally, so your Discord export stays on your computer.
</p>

<p align="center">
  <a href="https://github.com/ellypaws/unpackage/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/ellypaws/unpackage?include_prereleases&label=release&logo=github&color=5865f2"></a>
  <a href="https://github.com/ellypaws/unpackage/releases"><img alt="Downloads" src="https://img.shields.io/github/downloads/ellypaws/unpackage/total?label=downloads&logo=github&color=5865f2"></a>
  <a href="https://github.com/ellypaws/unpackage/actions"><img alt="Build" src="https://img.shields.io/github/actions/workflow/status/ellypaws/unpackage/build.yml?logo=githubactions&logoColor=white&label=build"></a>
  <a href="https://go.dev/"><img alt="Go 1.27+" src="https://img.shields.io/badge/go-1.27%2B-00ADD8?logo=go&logoColor=white"></a>
  <a href="https://github.com/ellypaws/unpackage"><img alt="Platform" src="https://img.shields.io/badge/platform-windows-0078d6?logo=windows&logoColor=white"></a>
  <br>
  <a href="https://github.com/ellypaws/unpackage/graphs/contributors"><img alt="Contributors" src="https://img.shields.io/github/contributors/ellypaws/unpackage"></a>
  <a href="https://github.com/ellypaws/unpackage/commits/main"><img alt="Commit activity" src="https://img.shields.io/github/commit-activity/m/ellypaws/unpackage"></a>
  <a href="https://github.com/ellypaws/unpackage/stargazers"><img alt="Stars" src="https://img.shields.io/github/stars/ellypaws/unpackage?style=social"></a>
</p>

<p align="center"><a href="https://github.com/ellypaws/unpackage/releases/latest"><img alt="Download for Windows" src="https://img.shields.io/badge/Download%20for%20Windows-5865f2?style=for-the-badge&logo=windows&logoColor=white"></a></p>

---

## What it does

Discord lets you request a copy of your account data. If you have an older and a newer copy,
unpackage compares the message IDs and shows what is present in the older package but missing
from the newer one. It helps you investigate an export change and prepare a reviewable deletion
request.

The app is offline. It does not sign in to Discord, upload your packages, send a request, or delete
messages. A missing message is a candidate for review, not proof that Discord deleted it.

If you only have one package, unpackage also reads `send_message` analytics records from
`Activity/reporting` and `Activity/tns`. These records can preserve a message ID, channel, server,
time, client, and counts after the message body is absent from `Messages`. They do not contain the
message text. The app labels them `send event only`, merges duplicate evidence by message ID, and
shows its source and reported metadata in the message details and structured exports.

## Download

> [!TIP]
> Download **`unpackage-<version>-win-x64.exe`** from [Releases](https://github.com/ellypaws/unpackage/releases/latest).
> It is a single Windows executable and needs no installer.

Windows 10 or newer is recommended. A terminal window should be at least 64 columns wide and 24
rows high for the full-screen interface.

## Quick start

1. Download the Windows executable from the release page.
2. Open a terminal in the folder where you saved it.
3. Start `unpackage.exe`.
4. Choose one Discord package, or choose older and newer package folders or ZIP files to compare.
5. Review the available message evidence, then narrow it by date, server, search text, or attachments.

The app keeps what it reads in memory for the current session. It does not change your source
packages. Close the app to clear the loaded data.

The Stats tab summarizes the loaded packages. Overview shows totals and top cards. Activity shows a
weekday by hour grid, a calendar, or a month grid for any metric, with hover or arrow-key details on
each cell and a choice of range, palette, cell style, and scale. Top shows cards of servers, channels,
conversations, games, platforms, and emoji ranked by several metrics. Open a card for the full ranking by
any metric. Hover a value for the exact figure, a duration in years, months, days, hours, and
minutes, and its share of the total. Opening a ranked item follows the context: a server shows its
channels by the same metric, a channel or conversation ranked by a message metric shows those messages
newest first with a jump to Investigate, and anything ranked by voice, play, reaction, stream, or
edit metrics shows its activity grid with a scope chip you can clear. Choices with more than two
options open as dropdown menus; hovering an option previews it, Enter or a click applies it, and Esc
or a click elsewhere closes it. Numbers that change flash by the size of the change and settle. Overview also reports words, links,
attachments, average length, active days, longest streak, longest and average voice session, and
edits and deletions recorded by Discord's own analytics. Ranges default to all time. Voice time, play time, app sessions, reactions,
streams, and joined servers come from the package's Activity folder and appear once it finishes
loading. Both packages are combined and identical events are counted once. Group conversations show
their custom name with the participants in parentheses, or the participants' global names when the
export includes them. The export does not list who was in a voice channel with you.

The server picker sorts by message count. Use the sort control to switch to name or missing count.
Activate a server once to include it, again to exclude it, and a third time to clear it. If any
servers are included, only those servers are eligible. Excluded servers are always removed.

Use `Paste from clipboard` in the Investigate tab after copying a Discord safety-notice message
response. This avoids terminals that replay Ctrl+V input synchronously. Recognized responses add
every `incident_time` to the exact-second message filter, so repeated pastes accumulate and
duplicate times are ignored. Clipboard contents are processed in memory and are not shown in the
command console or written to logs. On Linux the app tries `wl-paste`, `xclip`, `xsel`, and on WSL
`powershell.exe`, in that order, so install `wl-clipboard`, `xclip`, or `xsel` if none are present.
If the clipboard still cannot be read, type the unix seconds into the
date box instead, for example `1700000000; 1700000060`. Whole numbers above 100000 are treated as
unix times rather than days ago, and each entry is added to the exact-second filter.

The package browser keeps parent folders visible in columns. Hover a narrow column to expand it.
Type part of a folder name to filter and highlight fuzzy matches, then use `Tab` to complete the
first match. A trailing slash enters a directory. Single-click a folder to open it, or double-click
one to choose it for scanning. `Choose this package` selects the named folder or ZIP. Folders with
a `Messages/index.json` package structure are marked `Package`. Directory counts appear as they
finish and hide when space is limited.

The older and newer browsers share their starting location until each has a selected package.
After that, each remembers its own location for the current session.

## Try it without a Discord export

The sample command creates fictional packages for a safe walkthrough. The destination folder must
not already exist.

```powershell
unpackage.exe sample demo
unpackage.exe
```

The sample contains edited messages and changed attachment URLs so you can see that matching is
based on message IDs, not message text or attachment URLs.

## Useful commands

The full-screen app, console, and REPL use the same commands. In the Console tab or after starting
`unpackage.exe repl`, try:

```text
open older "C:\Exports\older.zip"
open newer "C:\Exports\newer"
status
mode missing
servers
select 300000000000000001
exclude 300000000000000002
dates "2022-11-19; 1,396 days ago"
search "some text"
list jsonl
```

Use `summary`, `leaders servers missing`, or `heatmap voice-time all` for statistics in the console.
Use `show MESSAGE_ID` for a complete row. Use `list jsonl` or `list tsv` to export the current
results. Use `clear` to reset filters and `stop` to stop an import while keeping data already read.

## Create a deletion-request draft

Include or exclude one or more server IDs, then choose the scope of the draft:

```text
select 300000000000000001 300000000000000002
exclude 300000000000000003
request "deletion-request.txt" filtered
```

`filtered` uses the current filters. `all` includes every observed message admitted by the server
selection. The draft contains server, channel, and message IDs for review. It does not include
message bodies or attachment URLs, and it is never sent automatically.

## Command-line comparison

For a script-friendly JSONL result:

```powershell
unpackage.exe diff "C:\Exports\older.zip" "C:\Exports\newer" > missing.jsonl
```

Add `--format tsv` for tab-separated output. You can also use `--server ID`,
`--exclude-server ID`, `--date DATE`, `--search TEXT`, `--media all|attachments|media`, and
`--mode missing|all|older|newer|present`.

## Keyboard shortcuts

`Tab` and `Shift+Tab` move focus. `Enter` activates a control. `Ctrl+Tab` switches tabs. In the
Stats tab, arrow keys move across the activity grid once it has focus and scroll the leader list. `F1`
opens help. `Ctrl+O`, `Ctrl+N`, and `Ctrl+D` open the older package, newer package, and calendar
controls. `Ctrl+X` stops an import, `Esc` closes an open panel, and `Ctrl+C` exits.

In the package browser, `Tab` completes the first match, `Ctrl+Tab` and `Shift+Tab` move focus,
and `Ctrl+O` chooses the selected package. `Alt+Up` opens the parent folder, `Alt+Left` and
`Alt+Right` move through history, and `Alt+Down` enters the highlighted row.

## Build from source

You need Go 1.27 or newer:

```powershell
$env:CGO_ENABLED = '0'
go build -o unpackage.exe ./cmd
.\unpackage.exe
```

## Privacy and limits

The program reads exported package files locally and stores parsed messages in memory. It creates
no database and does not make network requests at runtime. Logs record import status, not message
bodies, account names, package paths, or attachment URLs.

Discord export omissions can reflect a change in export scope or access. Compare the packages in
the order you provide them, and review every result before treating it as a deletion candidate.

## License

See the repository for the project license.
