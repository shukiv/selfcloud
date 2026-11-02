// SPDX-License-Identifier: AGPL-3.0-or-later

// The tabs of one browser talk to each other about updates (decision 65). A tab that is about to let a new version take over
// asks the others whether any of them is busy. Each tab answers for itself; the channel is the browser's own, and carries no
// person's data, only whether a tab is busy.

const CHANNEL = "selfcloud-update";

// startTab makes this tab answer the others' questions with its own busy state. It returns a function that stops it.
export function startTab(id: string, isBusy: () => boolean): () => void {
  if (typeof BroadcastChannel === "undefined") return () => undefined;
  const channel = new BroadcastChannel(CHANNEL);
  channel.onmessage = (event: MessageEvent) => {
    const msg = event.data as { type?: string; from?: string } | null;
    if (msg?.type === "busy?" && msg.from !== id) {
      channel.postMessage({ type: "busy", from: id, busy: isBusy() });
    }
  };
  return () => channel.close();
}

// askOtherTabsBusy asks the other tabs and waits timeoutMs for their answers. It is true when one of them is busy. A browser
// without channels has no other tabs to ask, so the answer is false.
export function askOtherTabsBusy(id: string, timeoutMs = 300): Promise<boolean> {
  if (typeof BroadcastChannel === "undefined") return Promise.resolve(false);
  return new Promise((resolve) => {
    const channel = new BroadcastChannel(CHANNEL);
    let busy = false;
    channel.onmessage = (event: MessageEvent) => {
      const msg = event.data as { type?: string; from?: string; busy?: boolean } | null;
      if (msg?.type === "busy" && msg.from !== id && msg.busy) busy = true;
    };
    channel.postMessage({ type: "busy?", from: id });
    setTimeout(() => {
      channel.close();
      resolve(busy);
    }, timeoutMs);
  });
}

// newTabId names this tab, so it does not answer its own question.
export function newTabId(): string {
  return Math.random().toString(36).slice(2) + Date.now().toString(36);
}
