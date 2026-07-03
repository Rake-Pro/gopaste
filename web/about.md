# gopaste

Sharing text and code should be fast and frictionless. gopaste is a small,
self-hosted pastebin: paste something, save it, and share the URL.

## Basic usage

Type or paste what you want to share, click **Save** (or press `Ctrl + S`),
then copy the URL from your browser. Send that URL and the recipient sees the
same content.

To start a new paste, click **New** (or press `Ctrl + N`).

While viewing a paste:

- **Raw** opens the plain-text version (also at `/api/pastes/<key>/raw`).
- **Duplicate** copies the content into a new, editable paste.
- **Copy link** puts the paste URL on your clipboard.

## From the console

You can post directly from a shell with `curl`:

```
curl --data-binary @file.txt https://paste.example.com/api/pastes
```

The response is JSON like `{"id":"tavokelu"}`; the paste is then at
`https://paste.example.com/<key>` (raw at `/api/pastes/<key>/raw`).

## Duration

Depending on configuration, pastes may expire after a period of inactivity.
There is no guarantee of retention; do not rely on a paste persisting, and post
at your own risk.

## Privacy

Pastes are not crawled by search engines that honor `robots.txt`, but there is
no expectation of privacy. Anyone with the URL can read the paste. Do not post
secrets or sensitive data.

## Themes

Use the theme switch in the status bar to toggle between the available themes.
Your choice is remembered on this device.
