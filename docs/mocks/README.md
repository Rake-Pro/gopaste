# gopaste UI mocks

Design previews for the frontend redesign. Each mock is a self-contained HTML
file under this directory, served read-only by the report-viewer at
`https://claude.rake.pro/reports/gopaste/docs/mocks/<file>`.

Purpose: review and approve major design aspects before they are implemented as
real `web/static` assets. The backend depends only on the wire contract
(`docs/DESIGN.md` section 2.2), so the UI can iterate freely here.

| Version | File | Direction | Status |
|---|---|---|---|
| v1 | `gopaste-ui-v1.html` | rake brand: tactical navy/steel-blue, HUD command bar, status bar, token-based theming | Approved |
| impl | `gopaste-ui-implemented.html` | Snapshot of the shipped frontend (links live `web/static/application.css`); rake + arctic themes | Current |

## Open approval items (v1)

- Layout: HUD command bar (proposed) vs a floating top-left control panel.
- Ambience: holographic grid + scanlines + glow (proposed) vs more restrained.
- Syntax theme: brand-derived tactical palette (proposed) vs keeping solarized.
- Action set: dropping Twitter for Copy link; dropping jQuery/CDN for vanilla.

Decisions get recorded in `../../CHANGELOG.md` and reflected in `../../BACKLOG.md`.
