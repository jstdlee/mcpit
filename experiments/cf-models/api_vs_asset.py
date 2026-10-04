#!/usr/bin/env python3
"""Can Clef sort captured network requests (DevTools Network panel style) into
api / asset / tracking / auth / other, in one batched call?
Usage: python3 api_vs_asset.py
"""
import json, os, sys, time
sys.path.insert(0, os.path.dirname(__file__))
from bench import run  # noqa: E402

PAGE = "https://shop.example.com/search?q=lamp"
# (entry, truth). Fields mirror what CDP Network.* events give: url, method, resourceType,
# mimeType, status, initiator, a short response preview.
ENTRIES = [
    ({"url": "https://shop.example.com/api/search?q=lamp&page=1", "method": "GET", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "search-box input handler", "preview": '{"results":[{"id":981,"title":"Desk lamp"}],"total":212}'}, "api"),
    ({"url": "https://shop.example.com/static/js/app.3f2a9c.js", "method": "GET", "type": "Script", "mime": "application/javascript", "status": 200, "initiator": "parser", "preview": "!function(e){var t={};function n(r){"}, "asset"),
    ({"url": "https://shop.example.com/fonts/inter-var.woff2", "method": "GET", "type": "Font", "mime": "font/woff2", "status": 200, "initiator": "stylesheet", "preview": ""}, "asset"),
    ({"url": "https://www.google-analytics.com/g/collect?v=2&tid=G-XYZ&en=page_view", "method": "POST", "type": "Ping", "mime": "text/plain", "status": 204, "initiator": "gtag.js", "preview": ""}, "tracking"),
    ({"url": "https://shop.example.com/graphql", "method": "POST", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "apollo client", "body": '{"operationName":"ProductFilters","variables":{"category":"lighting"}}', "preview": '{"data":{"filters":[{"name":"color","values":["black","white"]}]}}'}, "api"),
    ({"url": "https://shop.example.com/_next/data/b7Kx/product/981.json", "method": "GET", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "next router", "preview": '{"pageProps":{"product":{"id":981,"price":39.9}}}'}, "api"),
    ({"url": "https://shop.example.com/api/session/refresh", "method": "POST", "type": "XHR", "mime": "application/json", "status": 200, "initiator": "auth.js timer", "preview": '{"expires_in":900}'}, "auth"),
    ({"url": "https://o4500.ingest.sentry.io/api/123/envelope/", "method": "POST", "type": "Fetch", "mime": "text/plain", "status": 200, "initiator": "sentry sdk", "preview": ""}, "tracking"),
    ({"url": "https://cdn.shop.example.com/images/p/981-800.webp", "method": "GET", "type": "Image", "mime": "image/webp", "status": 200, "initiator": "img srcset", "preview": ""}, "asset"),
    ({"url": "https://shop.example.com/api/recommendations?pid=981&limit=8", "method": "GET", "type": "XHR", "mime": "application/json", "status": 200, "initiator": "product page carousel", "preview": '{"items":[{"id":1022,"title":"Floor lamp"}]}'}, "api"),
    ({"url": "https://shop.example.com/manifest.webmanifest", "method": "GET", "type": "Manifest", "mime": "application/manifest+json", "status": 200, "initiator": "link rel=manifest", "preview": '{"name":"Shop","icons":[...]}'}, "asset"),
    ({"url": "https://api.segment.io/v1/t", "method": "POST", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "analytics.js", "preview": '{"success":true}'}, "tracking"),
    ({"url": "https://shop.example.com/cdn-cgi/challenge-platform/h/b/orchestrate/jsch/v1", "method": "GET", "type": "Script", "mime": "application/javascript", "status": 200, "initiator": "parser", "preview": ""}, "other"),
    ({"url": "https://shop.example.com/api/i18n/en-US/common.json", "method": "GET", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "i18next", "preview": '{"add_to_cart":"Add to cart","search":"Search"}'}, "asset"),
    ({"url": "https://shop.example.com/api/stores/nearby?lat={lat}&lng={lng}", "method": "GET", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "store finder button", "preview": '{"stores":[{"name":"Downtown","distance_km":1.2}]}'}, "api"),
    ({"url": "https://shop.example.com/api/flags?client=web", "method": "GET", "type": "Fetch", "mime": "application/json", "status": 200, "initiator": "feature-flag sdk", "preview": '{"new_checkout":true,"dark_mode":false}'}, "other"),
]
CRITERIA = {
    "api": "A data endpoint a user task could call: search, list, detail, filter, lookup.",
    "asset": "A static file or UI text: script, style, font, image, manifest, translations.",
    "tracking": "Analytics, telemetry, error reporting or ads.",
    "auth": "Login, session or token handling.",
    "other": "Bot checks, feature flags or anything else.",
}


def main():
    state = {"page": PAGE, "requests": {f"r{i}": e for i, (e, _) in enumerate(ENTRIES)}}
    qs = {f"r{i}": {"type": "choice", "instructions": f"What is network request r{i}?", "criteria": CRITERIA} for i in range(len(ENTRIES))}
    for model in ["@cf/cloudflare/clef-flash", "@cf/cloudflare/clef"]:
        times, last = [], None
        for _ in range(3):
            res, ms = run(model, {"model": model.rsplit("/", 1)[1], "state": state, "questions": qs})
            times.append(ms); last = res
        ok, misses = 0, []
        for i, (_, truth) in enumerate(ENTRIES):
            a = last["answers"][f"r{i}"]
            if a["choice"] == truth:
                ok += 1
            else:
                misses.append(f"r{i} {ENTRIES[i][0]['url'][:50]} → {a['choice']} (want {truth}, conf {a.get('confidence')})")
        print(f"{model}: {ok}/{len(ENTRIES)} in one call of {len(ENTRIES)} questions, "
              f"median {sorted(times)[1]:.0f} ms, tokens {last.get('usage', {}).get('input_tokens')}")
        for m in misses:
            print("   miss", m)


if __name__ == "__main__":
    main()
