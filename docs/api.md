# tunwatch API v1

Base URL: `http://127.0.0.1:8080`（既定）。時刻は RFC 3339 UTC です。クライアント情報の API はすべて `Authorization: Bearer <TUNWATCH_API_TOKEN>` または Web UI のセッション cookie が必要です。

| Method / path | 応答 |
| --- | --- |
| `GET /healthz` | HTTP プロセスの生存確認。認証不要、メタデータなし |
| `GET /readyz` | 監視準備完了時 200、未完了時 503。認証不要 |
| `GET /api/v1/devices` | `{ "time": "...", "devices": [...] }` |
| `GET /api/v1/vips[?ip=<IPv4>]` | `{ "time": "...", "vips": [...] }`。観測した VRRP VIP の一覧。`ip` で特定の IPv4 に絞り込み可能 |
| `GET /api/v1/events` | `{ "events": [...], "next_cursor": "..." }`、新しい順 |
| `GET /api/v1/status` | readiness、`node_name`、解決済みの設定、件数、捕捉・通知のカウンター。秘密情報や他ノードの profile は含めない |
| `GET /api/v1/stream` | SSE `snapshot` イベント、2 秒間隔。最大 16 接続 |
| `POST /api/v1/session` | JSON `{ "token": "..." }` から cookie を設定、204 |
| `DELETE /api/v1/session` | cookie を削除、204 |

## Device

```json
{
  "id": "eth0//00:11:22:33:44:55",
  "interface": "eth0",
  "mac": "00:11:22:33:44:55",
  "ips": ["192.0.2.10"],
  "vip_only": false,
  "hostname": {"value": "workstation.local", "source": "mdns", "confidence": "medium"},
  "vendor": "Example vendor",
  "os": {"value": "Unix / mobile 系の可能性", "source": "ip.ttl", "confidence": "low"},
  "local_mac": false,
  "status": "online",
  "first_seen": "2026-10-09T12:00:00Z",
  "last_seen": "2026-10-09T12:01:00Z",
  "sources": ["arp", "mdns"]
}
```

ID は `interface/VLAN-stack/MAC` です。VLAN がないときは中央が空になります。VLAN がある場合は `vlan` フィールドも返します。ID の `/` などを query string に渡す場合は URL encode してください。同一機器の MAC ランダム化やインターフェース移動を自動で統合しません。`ips` は過去の観測値も含みます。

`status`: `online`、`stale`、`offline`、`unknown`。`offline` は無通信による推定です。`hostname` / `os` の未取得時は各値が空文字です。`vendor` は未取得時省略します。

`vips` はこの端末から観測した、または同じインターフェース・VLAN の IP 照合で分かった VIP の配列です。各要素は `ip`、`source`（`vrrp_advertisement` または `vrrp_mac`）、`vrid` を含みます。`vip_only: true` は VRRP 仮想 MAC、または観測した IP がすべて VIP であることを示します。Web UI はこの行を既定で隠しますが、`GET /api/v1/devices` と SSE は全端末を返します。`GET /api/v1/vips` は、端末の通常 IP としてまだ観測していない広告中の VIP も `interface`、任意の `vlan`、`ip`、`source`、`vrid` とともに返します。判定は受動観測に基づき、未観測の VIP を網羅しません。

## 履歴

`GET /api/v1/events?limit=100&device=<encoded-device-id>&before=<cursor>`

- `limit`: 既定 100、1–1000。
- `device`: 任意の端末 ID 完全一致。
- `before`: 前ページの `next_cursor`。排他的な cursor です。
- `next_cursor` が空になるまでページを取得します。件数が limit と一致した場合、最後に空ページが返る場合があります。
- 同時実行は 2 リクエストまで。保存期間・容量によって古い履歴は削除されるため、cursor は永久に有効なアーカイブを示しません。

各イベントは `id`、`time`、`type`、`reason`、**その時点の** `device` を含みます。type は `online`、`stale`、`offline`、`updated`。イベント ID は opaque string として扱ってください。

## SSE

```text
event: snapshot
data: {"time":"2026-10-09T12:01:00Z","ready":true,"devices":[...]}

```

差分配信ではなく最新スナップショットです。切断時は再接続し、受信した配列でローカル状態を置き換えます。イベント履歴の完全な消費には `/events` を併用してください。`Last-Event-ID` による SSE 履歴再送はありません。ブラウザは同一 origin の cookie 認証で接続します。Web UI はログイン成功時にトークンを localStorage に保存し、401 応答時に `/session` で cookie を再取得します。保存トークンが拒否された場合は削除し、ログイン画面に戻ります。URL query に token を付ける方式はサポートしません。

## エラー

- `400`: 不正な query parameter
- `401`: token / session が不正または期限切れ
- `404`: 未知の API
- `415`: login の Content-Type が `application/json` ではない
- `429`: login（全体 10 回 / 秒）または履歴取得の同時実行制限
- `503`: readiness、ストリーム上限、履歴ストレージの問題

エラー本文は短い plain text です。API は読み取り中心で、設定や任意コマンド実行の endpoint はありません。

`GET /api/v1/status` の `capture.interfaces[]` には `probe_targets`（ARP 探索対象数）、`arp_sent` / `mdns_sent`（起動後の送信数）を含めます。送信上限と周期は `config.probe_rate` / `config.scan_interval`、mDNS の有効・無効は `config.mdns_lookup` で確認できます。

`devices[].hosts_name` は任意の hosts ファイル照合による表示名です。`value`、`source: "hosts_file"`、`confidence: "configured"`、`detail`（ファイル名と照合 IP）を含みます。同じ MAC に複数の IPv4 アドレスがある場合、まず `gw` を含む hosts 名を優先します（大文字・小文字は区別しません）。同じ優先順位の候補が複数ある場合は数値上もっとも若い名前付き IPv4 アドレスの名前を採用します。該当なし、または複数 IPv4 による優先順位を適用できない曖昧な照合では値が空です。`hostname` は従来どおりネットワークで取得した名前を保持します。イベントにも両方の名前を記録します。

GitHub 自動取得時は `capture.hosts_sync` に `url`、`entries`、`last_checked`、`last_success`、`error` を返します。取得失敗時も稼働中の直前の照合を保持します。`hosts_name.source` は引き続き `hosts_file`、`detail` には取得元 URL と照合 IP を記録します。
