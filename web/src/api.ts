const tokenKey = "tunwatch.api_token";
let renewal: Promise<boolean> | undefined;
let renewalController: AbortController | undefined;
let generation = 0;

export class APIError extends Error {
  constructor(public status: number) {
    super(status === 401 ? "認証が必要です" : `取得に失敗しました (${status})`);
  }
}

export function savedToken(): string | null {
  try {
    return localStorage.getItem(tokenKey);
  } catch {
    return null;
  }
}

export function forgetToken() {
  generation++;
  renewalController?.abort();
  renewal = undefined;
  try {
    localStorage.removeItem(tokenKey);
  } catch {
    /* Storage may be disabled. */
  }
}

export function rememberToken(token: string): boolean {
  forgetToken();
  try {
    localStorage.setItem(tokenKey, token);
    return true;
  } catch {
    return false;
  }
}

function request(path: string, init?: RequestInit) {
  return fetch("/api/v1/" + path, {
    signal: AbortSignal.timeout(10000),
    ...init,
  });
}

function renewSession(): Promise<boolean> {
  if (renewal) return renewal;
  const token = savedToken();
  if (!token) return Promise.resolve(false);
  const version = generation;
  const controller = new AbortController();
  renewalController = controller;
  const pending = (async () => {
    const response = await request("session", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token }),
      signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
    });
    if (version !== generation) return false;
    if (response.status === 401) {
      if (savedToken() === token) forgetToken();
      return false;
    }
    if (!response.ok) throw new APIError(response.status);
    return true;
  })();
  renewal = pending;
  // Concurrent API/SSE retries share a single renewal request.
  void pending
    .finally(() => {
      if (renewal === pending) {
        renewal = undefined;
        renewalController = undefined;
      }
    })
    .catch(() => {});
  return pending;
}

export async function api(path: string, init?: RequestInit) {
  let response = await request(path, init);
  if (response.status === 401 && path !== "session" && (await renewSession())) {
    response = await request(path, init);
  }
  if (!response.ok) throw new APIError(response.status);
  return response.status === 204 ? null : response.json();
}
