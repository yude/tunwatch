import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

type Hint = { value: string; source: string; confidence: string };
type Device = {
  id: string;
  interface: string;
  vlan?: string;
  mac: string;
  ips: string[];
  hostname: Hint;
  vendor?: string;
  os: Hint;
  local_mac: boolean;
  status: string;
  first_seen: string;
  last_seen: string;
  sources: string[];
};
type Event = {
  id: string;
  time: string;
  type: string;
  reason: string;
  device: Device;
};
type Status = {
  node_name: string;
  ready: boolean;
  capacity_rejections: number;
  config: {
    interfaces: string[];
    networks: string[];
    active_arp: boolean;
    reverse_dns: boolean;
    stale_after: string;
    offline_after: string;
    retention_days: number;
    history_max_mb: number;
    max_devices: number;
    scan_interval: string;
  };
  capture: {
    demo: boolean;
    version: string;
    interfaces: {
      interface: string;
      observations: number;
      queue_dropped: number;
      kernel_dropped: number;
      probe_targets: number;
    }[];
    notifications?: { sent: number; failed: number; dropped: number };
  };
};
const labels: Record<string, string> = {
  online: "観測中",
  stale: "未確認",
  offline: "オフライン推定",
  unknown: "再観測待ち",
  updated: "情報を更新",
};
const format = (s: string) =>
  new Date(s).toLocaleString("ja-JP", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
const age = (s: string) => {
  const n = Math.max(
    0,
    Math.floor((Date.now() - new Date(s).getTime()) / 1000),
  );
  return n < 60
    ? `${n} 秒前`
    : n < 3600
      ? `${Math.floor(n / 60)} 分前`
      : `${Math.floor(n / 3600)} 時間前`;
};
function Icon({ name, size = 20 }: { name: string; size?: number }) {
  const paths: Record<string, React.ReactNode> = {
    network: (
      <>
        <rect x="3" y="3" width="6" height="6" rx="1.5" />
        <rect x="15" y="15" width="6" height="6" rx="1.5" />
        <path d="M6 9v9h9M9 6h9v9" />
      </>
    ),
    devices: (
      <>
        <rect x="3" y="4" width="18" height="13" rx="2" />
        <path d="M8 21h8m-4-4v4" />
      </>
    ),
    history: (
      <>
        <path d="M3 10a9 9 0 1 1 2 8M3 4v6h6m3-4v6l4 2" />
      </>
    ),
    settings: (
      <>
        <path d="M4 7h16M4 17h16" />
        <circle cx="9" cy="7" r="3" />
        <circle cx="16" cy="17" r="3" />
      </>
    ),
    search: (
      <>
        <circle cx="10" cy="10" r="6" />
        <path d="m15 15 5 5" />
      </>
    ),
    arrow: <path d="m9 5 7 7-7 7" />,
    pulse: <path d="M2 12h5l3-8 4 16 3-8h5" />,
    close: <path d="m6 6 12 12M6 18 18 6" />,
    logout: (
      <>
        <path d="M9 3H4v18h5m4-14 5 5-5 5m-6-5h15" />
      </>
    ),
  };
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {paths[name] || paths.devices}
    </svg>
  );
}
function Badge({ status }: { status: string }) {
  return (
    <span className={`badge ${status}`}>
      <i />
      {labels[status] || status}
    </span>
  );
}
async function api(path: string, init?: RequestInit) {
  const r = await fetch("/api/v1/" + path, {
    signal: AbortSignal.timeout(10000),
    ...init,
  });
  if (!r.ok)
    throw new Error(
      r.status === 401 ? "認証が必要です" : `取得に失敗しました (${r.status})`,
    );
  return r.status === 204 ? null : r.json();
}
function App() {
  const [logged, setLogged] = useState(false),
    [checking, setChecking] = useState(true),
    [token, setToken] = useState(""),
    [error, setError] = useState(""),
    [devices, setDevices] = useState<Device[]>([]),
    [status, setStatus] = useState<Status | null>(null),
    [events, setEvents] = useState<Event[]>([]),
    [cursor, setCursor] = useState(""),
    [live, setLive] = useState(false),
    [updated, setUpdated] = useState(""),
    [page, setPage] = useState("devices"),
    [query, setQuery] = useState(""),
    [filter, setFilter] = useState("all"),
    [selected, setSelected] = useState<Device | null>(null),
    [loading, setLoading] = useState(false);
  useEffect(() => {
    api("status")
      .then((s) => {
        setStatus(s);
        setLogged(true);
      })
      .catch(() => {})
      .finally(() => setChecking(false));
  }, []);
  async function loadEvents(more = false) {
    setLoading(true);
    try {
      const d = await api(
        "events?limit=50" +
          (more ? "&before=" + encodeURIComponent(cursor) : ""),
      );
      setEvents((old) => (more ? [...old, ...d.events] : d.events));
      setCursor(d.next_cursor);
      setError("");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    if (!logged) return;
    let active = true;
    const es = new EventSource("/api/v1/stream");
    es.addEventListener("snapshot", (e) => {
      const data = JSON.parse((e as MessageEvent).data);
      setDevices(data.devices);
      setUpdated(data.time);
      setLive(data.ready);
    });
    es.onerror = () => {
      setLive(false);
      api("status").catch((e) => {
        if (active) {
          setError(e.message);
          if (e.message === "認証が必要です") setLogged(false);
        }
      });
    };
    const updateStatus = () =>
      api("status")
        .then((s) => {
          if (active) setStatus(s);
        })
        .catch(() => {});
    updateStatus();
    const timer = setInterval(updateStatus, 10000);
    return () => {
      active = false;
      es.close();
      clearInterval(timer);
    };
  }, [logged]);
  useEffect(() => {
    if (logged && page === "history") void loadEvents();
  }, [logged, page]);
  useEffect(() => {
    if (!selected) return;
    const fresh = devices.find((d) => d.id === selected.id);
    if (fresh) setSelected(fresh);
  }, [devices]);
  async function login(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await api("session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      });
      setToken("");
      setLogged(true);
    } catch (e) {
      setError((e as Error).message);
    }
  }
  async function logout() {
    await api("session", { method: "DELETE" });
    setLogged(false);
    setDevices([]);
    setEvents([]);
    setSelected(null);
    setStatus(null);
    setLive(false);
  }
  const counts = devices.reduce(
    (a, d) => ({ ...a, [d.status]: (a[d.status] || 0) + 1 }),
    {} as Record<string, number>,
  );
  const filtered = devices.filter(
    (d) =>
      (filter === "all" || d.status === filter) &&
      [d.hostname.value, d.mac, d.vendor, ...d.ips, d.interface]
        .join(" ")
        .toLowerCase()
        .includes(query.toLowerCase()),
  );
  if (checking)
    return (
      <div className="login-screen">
        <span className="muted">tunwatch に接続しています…</span>
      </div>
    );
  if (!logged)
    return (
      <main className="login-screen">
        <form onSubmit={login} className="login-card">
          <div className="brand">
            <span className="brand-icon">
              <Icon name="network" size={25} />
            </span>
            tunwatch<span className="brand-dot">.</span>
          </div>
          <div className="eyebrow">YOUR NETWORK, IN VIEW</div>
          <h1>
            ネットワークの今を、
            <br />
            ひと目で。
          </h1>
          <p className="muted">端末の存在と変化を、静かに見守ります。</p>
          <label htmlFor="token">アクセストークン</label>
          <input
            id="token"
            type="password"
            autoComplete="current-password"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            placeholder="TUNWATCH_API_TOKEN"
            required
          />
          <button className="primary" type="submit">
            ダッシュボードを開く <Icon name="arrow" size={16} />
          </button>
          {error && (
            <p role="alert" className="error">
              {error}
            </p>
          )}
          <small>トークンはブラウザのストレージに保存されません。</small>
        </form>
      </main>
    );
  return (
    <div className="shell">
      <aside className="sidebar">
        <a
          className="brand"
          href="#"
          onClick={(e) => {
            e.preventDefault();
            setPage("devices");
          }}
        >
          <span className="brand-icon">
            <Icon name="network" size={25} />
          </span>
          tunwatch<span className="brand-dot">.</span>
        </a>
        <div className="workspace">
          <span className="workspace-mark">L2</span>
          <div>
            Local network
            <small>
              {status?.capture.demo
                ? "デモ環境"
                : status?.config.interfaces?.join(", ") ||
                  "監視インターフェース"}
            </small>
          </div>
        </div>
        <p className="nav-label">WORKSPACE</p>
        <nav>
          {[
            ["devices", "クライアント"],
            ["history", "接続履歴"],
            ["settings", "監視設定"],
          ].map(([id, label]) => (
            <button
              key={id}
              className={page === id ? "active" : ""}
              onClick={() => setPage(id)}
            >
              <Icon name={id} />
              {label}
              {id === "devices" && (
                <span className="nav-count">{devices.length}</span>
              )}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="collector">
            <span className={"signal " + (live ? "good" : "bad")} />
            <div>
              {live ? "センサー稼働中" : "接続を確認中"}
              <small>{status?.capture.version || "tunwatch"}</small>
            </div>
          </div>
          <button className="logout" onClick={() => void logout()}>
            <Icon name="logout" size={17} />
            ログアウト
          </button>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <span>
            ワークスペース <span className="slash">/</span>{" "}
            <strong>
              {page === "devices"
                ? "クライアント"
                : page === "history"
                  ? "接続履歴"
                  : "監視設定"}
            </strong>
          </span>
          <span className={"live-pill " + (live ? "" : "disconnected")}>
            <i />
            {live ? "LIVE" : "再接続中"}
          </span>
        </header>
        <div className="content">
          <div className="heading">
            <div>
              <div className="eyebrow">NETWORK OBSERVABILITY</div>
              <h1>
                {page === "devices"
                  ? "ネットワークの現在地"
                  : page === "history"
                    ? "変化を、記録する。"
                    : "監視の設定"}
              </h1>
              <p className="muted">
                {page === "devices"
                  ? "つながる端末と、その小さな変化を見逃さない。"
                  : page === "history"
                    ? "観測開始から状態の変化まで、時系列で振り返ります。"
                    : "監視範囲・検出ポリシー・データの保存を確認できます。"}
              </p>
            </div>
            <div className="last-updated">
              最終更新<span>{updated ? format(updated) : "接続待ち"}</span>
            </div>
          </div>
          {status?.capture.demo && (
            <div className="notice demo">
              DEMO{" "}
              <span>
                表示中の端末は模擬データです。ネットワーク監視・通知送信は行いません。
              </span>
            </div>
          )}
          {error && (
            <div className="notice error" role="alert">
              {error}
            </div>
          )}
          {!live && (
            <div className="notice">
              ライブ接続を確認しています。表示内容は最新ではない可能性があります。
            </div>
          )}
          {page === "devices" && (
            <>
              <section className="metrics">
                {[
                  ["online", "観測中の端末", "直近の通信を確認"],
                  ["stale", "未確認", "しばらく通信がありません"],
                  ["offline", "オフライン推定", "無通信タイムアウト"],
                  ["all", "検出した端末", "保存期間内の合計"],
                ].map(([key, label, sub]) => (
                  <button
                    key={key}
                    className={"metric " + (filter === key ? "selected" : "")}
                    onClick={() => setFilter(key)}
                  >
                    <span className="metric-title">
                      {label}
                      <Icon
                        name={key === "online" ? "pulse" : "devices"}
                        size={18}
                      />
                    </span>
                    <strong className={key === "online" ? "mint" : ""}>
                      {key === "all" ? devices.length : counts[key] || 0}
                      <small>台</small>
                    </strong>
                    <span className="metric-sub">
                      <i className={key} />
                      {sub}
                    </span>
                  </button>
                ))}
              </section>
              <section className="panel">
                <div className="panel-heading">
                  <div>
                    <h2>クライアント</h2>
                    <span className="muted">{filtered.length} 台の端末</span>
                  </div>
                  <div className="search">
                    <Icon name="search" size={17} />
                    <input
                      aria-label="クライアントを検索"
                      placeholder="ホスト名、IP、MAC で検索"
                      value={query}
                      onChange={(e) => setQuery(e.target.value)}
                    />
                  </div>
                </div>
                <div className="filters">
                  {[
                    ["all", "すべて"],
                    ["online", "観測中"],
                    ["stale", "未確認"],
                    ["offline", "オフライン推定"],
                    ["unknown", "再観測待ち"],
                  ].map(([id, label]) => (
                    <button
                      key={id}
                      className={filter === id ? "active" : ""}
                      onClick={() => setFilter(id)}
                    >
                      {label}
                      {id === "all" && <span>{devices.length}</span>}
                    </button>
                  ))}
                </div>
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>デバイス</th>
                        <th>IP / MAC アドレス</th>
                        <th>ステータス</th>
                        <th>インターフェース</th>
                        <th>最終観測</th>
                        <th />
                      </tr>
                    </thead>
                    <tbody>
                      {filtered.map((d) => (
                        <tr key={d.id} onClick={() => setSelected(d)}>
                          <td>
                            <div className="device-name">
                              <span className="device-icon">
                                <Icon
                                  name={
                                    d.hostname.value.includes("server")
                                      ? "network"
                                      : "devices"
                                  }
                                />
                              </span>
                              <div>
                                <button
                                  className="device-link"
                                  onClick={() => setSelected(d)}
                                >
                                  {d.hostname.value || "名前未取得の端末"}
                                </button>
                                <small>
                                  {d.vendor ||
                                    (d.local_mac
                                      ? "ローカル管理 MAC"
                                      : "ベンダー不明")}
                                </small>
                              </div>
                            </div>
                          </td>
                          <td className="mono">
                            {d.ips[0] || "IP 未取得"}
                            {d.ips.length > 1 && (
                              <span className="extra">+{d.ips.length - 1}</span>
                            )}
                            <small>{d.mac}</small>
                          </td>
                          <td>
                            <Badge status={d.status} />
                          </td>
                          <td>
                            <span className="iface">
                              {d.interface}
                              {d.vlan && ` · VLAN ${d.vlan}`}
                            </span>
                          </td>
                          <td
                            className="muted nowrap"
                            title={format(d.last_seen)}
                          >
                            {age(d.last_seen)}
                          </td>
                          <td>
                            <Icon name="arrow" size={14} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {filtered.length === 0 && (
                    <div className="empty">
                      <Icon name="network" size={32} />
                      <h3>
                        {query
                          ? "一致する端末がありません"
                          : "まだ端末は観測されていません"}
                      </h3>
                      <p>
                        監視範囲とインターフェースを確認し、通信の受信をお待ちください。
                      </p>
                    </div>
                  )}
                </div>
                <div className="table-footer">
                  <span>
                    <i className="tiny-dot" />2 秒ごとに自動更新
                  </span>
                  <span>パケット本文は保存しません</span>
                </div>
              </section>
              <p className="footnote">
                「オフライン推定」は無通信による推定です。スリープ中の端末や、センサーから見えない通信もあります。
              </p>
            </>
          )}
          {page === "history" && (
            <section className="panel">
              <div className="panel-heading">
                <div>
                  <h2>接続イベント</h2>
                  <span className="muted">状態の変化のみを圧縮保存</span>
                </div>
                <button
                  className="secondary"
                  onClick={() => void loadEvents()}
                  disabled={loading}
                >
                  更新
                </button>
              </div>
              <div className="timeline">
                {events.map((e) => (
                  <div className="event" key={e.id}>
                    <span className={"event-dot " + e.type} />
                    <div className="event-copy">
                      <button
                        className="device-link"
                        onClick={() =>
                          setSelected(
                            devices.find((d) => d.id === e.device.id) ||
                              e.device,
                          )
                        }
                      >
                        {e.device.hostname.value || e.device.mac}
                      </button>
                      <span>{labels[e.type] || e.type}</span>
                      <small className="mono">
                        {e.device.mac} · {e.device.interface}
                      </small>
                    </div>
                    <time>{format(e.time)}</time>
                  </div>
                ))}
                {events.length === 0 && (
                  <div className="empty">
                    <Icon name="history" size={32} />
                    <h3>接続履歴はまだありません</h3>
                    <p>端末を観測すると、ここに記録が追加されます。</p>
                  </div>
                )}
              </div>
              {cursor && (
                <div className="load-more">
                  <button
                    className="secondary"
                    disabled={loading}
                    onClick={() => void loadEvents(true)}
                  >
                    {loading ? "取得中…" : "以前の履歴を読み込む"}
                  </button>
                </div>
              )}
            </section>
          )}
          {page === "settings" && status && (
            <>
              <div className="settings-grid">
                <section className="panel setting">
                  <h2>監視範囲</h2>
                  <dl>
                    <dt>実行ノード</dt>
                    <dd>{status.node_name || "未指定（共通設定）"}</dd>
                    <dt>インターフェース</dt>
                    <dd>{status.config.interfaces?.join(", ") || "demo0"}</dd>
                    <dt>対象ネットワーク</dt>
                    <dd>
                      {status.config.networks?.join(", ") ||
                        "インターフェースの直結ネットワーク"}
                    </dd>
                    <dt>アクティブ ARP 探索</dt>
                    <dd>
                      {status.config.active_arp ? "有効" : "無効（受動監視）"}
                    </dd>
                    <dt>逆引き DNS</dt>
                    <dd>{status.config.reverse_dns ? "有効" : "無効"}</dd>
                  </dl>
                </section>
                <section className="panel setting">
                  <h2>状態判定と保存</h2>
                  <dl>
                    <dt>未確認まで</dt>
                    <dd>{status.config.stale_after}</dd>
                    <dt>オフライン推定まで</dt>
                    <dd>{status.config.offline_after}</dd>
                    <dt>履歴の保存期間</dt>
                    <dd>{status.config.retention_days} 日</dd>
                    <dt>圧縮履歴の上限</dt>
                    <dd>{status.config.history_max_mb} MiB</dd>
                    <dt>端末数の上限</dt>
                    <dd>{status.config.max_devices}</dd>
                  </dl>
                </section>
                <section className="panel setting">
                  <h2>センサーの状態</h2>
                  {status.capture.interfaces.map((i) => (
                    <dl key={i.interface}>
                      <dt>{i.interface} · 観測数</dt>
                      <dd>{i.observations}</dd>
                      <dt>受信キューの破棄数</dt>
                      <dd>{i.queue_dropped}</dd>
                      <dt>カーネルの破棄数</dt>
                      <dd>{i.kernel_dropped}</dd>
                      <dt>ARP 探索対象</dt>
                      <dd>{i.probe_targets}</dd>
                    </dl>
                  ))}
                  <dl>
                    <dt>端末数上限による破棄</dt>
                    <dd>{status.capacity_rejections}</dd>
                  </dl>
                  <p className="muted">
                    破棄が増える場合は監視範囲やリソースを見直してください。
                  </p>
                </section>
                <section className="panel setting">
                  <h2>Discord 通知</h2>
                  {status.capture.notifications ? (
                    <dl>
                      <dt>送信済み</dt>
                      <dd>{status.capture.notifications.sent}</dd>
                      <dt>送信失敗</dt>
                      <dd>{status.capture.notifications.failed}</dd>
                      <dt>キューの破棄数</dt>
                      <dd>{status.capture.notifications.dropped}</dd>
                    </dl>
                  ) : (
                    <p className="muted">
                      通知先が未設定、またはデモモードです。
                    </p>
                  )}
                  <p className="muted">
                    通知には MAC・インターフェース・時刻のみを含めます。
                  </p>
                </section>
              </div>
              <div className="notice">
                設定変更は config.json
                と環境変数で行い、サービスを再起動してください。Kubernetes では
                ConfigMap と Secret を使用します。
              </div>
            </>
          )}
          <footer>
            tunwatch <span>·</span> A quieter way to watch your network.
          </footer>
        </div>
      </main>
      {selected && (
        <div className="modal-backdrop" onClick={() => setSelected(null)}>
          <section
            className="detail panel"
            role="dialog"
            aria-modal="true"
            aria-label="端末の詳細"
            onClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => {
              if (e.key === "Escape") setSelected(null);
            }}
          >
            <button
              className="close"
              autoFocus
              onClick={() => setSelected(null)}
              aria-label="閉じる"
            >
              <Icon name="close" />
            </button>
            <div className="eyebrow">DEVICE DETAILS</div>
            <h2>{selected.hostname.value || "名前未取得の端末"}</h2>
            <Badge status={selected.status} />
            <dl>
              <dt>MAC アドレス</dt>
              <dd className="mono">{selected.mac}</dd>
              <dt>IP アドレス（観測済み）</dt>
              <dd className="mono">{selected.ips.join(", ") || "未取得"}</dd>
              <dt>インターフェース</dt>
              <dd>
                {selected.interface}
                {selected.vlan && ` / VLAN ${selected.vlan}`}
              </dd>
              <dt>ベンダー</dt>
              <dd>
                {selected.vendor ||
                  (selected.local_mac
                    ? "ローカル管理 MAC：ベンダー判定対象外"
                    : "不明")}
              </dd>
              <dt>OS の推定</dt>
              <dd>
                {selected.os.value || "不明"}
                {selected.os.value && (
                  <small>
                    根拠: {selected.os.source} / 確度: {selected.os.confidence}
                  </small>
                )}
              </dd>
              <dt>ホスト名の根拠</dt>
              <dd>{selected.hostname.source || "未取得"}</dd>
              <dt>検出方式</dt>
              <dd>{selected.sources.join(" · ")}</dd>
              <dt>初回観測</dt>
              <dd>{format(selected.first_seen)}</dd>
              <dt>最終観測</dt>
              <dd>{format(selected.last_seen)}</dd>
            </dl>
            <p className="footnote">
              MAC・ホスト名は端末の自己申告を含みます。機器の真正性を保証するものではありません。
            </p>
          </section>
        </div>
      )}
    </div>
  );
}
createRoot(document.getElementById("root")!).render(<App />);
