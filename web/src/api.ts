export type APIError = Error & { code?: string };

export async function request<T>(
  path: string,
  options: RequestInit = {},
  csrfToken?: string,
): Promise<T> {
  const headers = new Headers(options.headers);
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  if (options.body) headers.set("Content-Type", "application/json");
  const send = () => fetch(path, { ...options, headers, credentials: "same-origin" });
  let response: Response;
  try {
    response = await send();
  } catch (reason) {
    // A reused keep-alive connection can be closed by the server just as it is
    // used; reads are safe to repeat once.
    if (options.method && options.method !== "GET") throw networkError();
    await new Promise((resolve) => window.setTimeout(resolve, 300));
    try {
      response = await send();
    } catch {
      throw networkError();
    }
  }
  const payload = await response.json();
  if (!response.ok) {
    const error = new Error(payload.message ?? "The request failed.") as APIError;
    error.code = payload.error;
    throw error;
  }
  return payload as T;
}

function networkError(): Error {
  return new Error("Could not reach the PR Inbox server. If it was stopped, restart it and open the new launch link; otherwise try again.");
}
