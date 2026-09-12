# MCPfier Extension 0.1.0

Chrome extension prototype for MCPfier. It discovers website capabilities,
exposes selected read-only adapters, and connects to a local MCPfier bridge.

## Files

- `manifest.json` - Chrome Manifest V3 extension manifest.
- `background.js` - service worker for tab access, bridge connection, and tool calls.
- `page-runtime.js` - page-side runtime for analysis, adapter registration, discovery, and calls.
- `popup.html`, `popup.js` - extension popup for enabling sites and selecting capabilities.
- `options.html`, `options.js` - bridge connection settings.
- `ui.css`, `theme.css` - extension UI styles.

## Local Install

1. Open `chrome://extensions/`.
2. Enable Developer mode.
3. Choose Load unpacked.
4. Select this folder: `mcpfier-extension-0.1.0`.

The extension requires Chrome 116 or newer.
