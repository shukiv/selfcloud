// SPDX-License-Identifier: AGPL-3.0-or-later

// The Web Audio graph: one shared context, one source per element, and a source only on a running context and a
// same-origin element. A fake AudioContext records every call, so the chain, the filters and the resume tries are
// judged without sound.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BANDS } from "./equalizer";

class FakeParam {
  value = 0;
}

class FakeNode {
  out: FakeNode[] = [];
  connect(next: FakeNode): FakeNode {
    this.out.push(next);
    return next;
  }
}

class FakeFilter extends FakeNode {
  type = "lowpass";
  frequency = new FakeParam();
  Q = new FakeParam();
  gain = new FakeParam();
}

class FakeSource extends FakeNode {
  el: HTMLMediaElement;
  constructor(el: HTMLMediaElement) {
    super();
    this.el = el;
  }
}

class FakeContext {
  static made: FakeContext[] = [];
  static resumeTo = "running";
  state = "suspended";
  resumes = 0;
  sources: FakeSource[] = [];
  filters: FakeFilter[] = [];
  destination = new FakeNode();
  onstatechange: (() => void) | null = null;
  constructor() {
    FakeContext.made.push(this);
  }
  async resume(): Promise<void> {
    this.resumes++;
    this.state = FakeContext.resumeTo;
  }
  createMediaElementSource(el: HTMLMediaElement): FakeSource {
    const s = new FakeSource(el);
    this.sources.push(s);
    return s;
  }
  createBiquadFilter(): FakeFilter {
    const f = new FakeFilter();
    this.filters.push(f);
    return f;
  }
}

// el builds a media element whose source and paused state are what the test says, as the other media tests do.
function el(src = "/a.ogg", paused = true): HTMLMediaElement {
  const m = document.createElement("audio");
  Object.defineProperty(m, "currentSrc", { value: new URL(src, location.href).href, configurable: true });
  Object.defineProperty(m, "paused", { value: paused, configurable: true });
  return m;
}

// load gives the module fresh state for each test: the shared context and the per-element sources live in the module.
async function load() {
  vi.resetModules();
  return import("./eqGraph");
}

beforeEach(() => {
  FakeContext.made = [];
  FakeContext.resumeTo = "running";
  vi.stubGlobal("AudioContext", FakeContext);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("attach", () => {
  it("makes no source while resume leaves the context suspended", async () => {
    FakeContext.resumeTo = "suspended";
    const g = await load();
    expect(await g.attach(el())).toBe(false);
    expect(FakeContext.made.length).toBe(1);
    expect(FakeContext.made[0].sources.length).toBe(0);
    expect(FakeContext.made[0].resumes).toBe(1);
  });

  it("makes no source and no context for an element of another origin", async () => {
    const g = await load();
    expect(await g.attach(el("https://elsewhere.example/a.ogg"))).toBe(false);
    expect(FakeContext.made.length).toBe(0);
  });

  it("makes one source per element across two attach calls, on one shared context", async () => {
    const g = await load();
    const m = el();
    expect(await g.attach(m)).toBe(true);
    expect(await g.attach(m)).toBe(true);
    expect(await g.attach(el("/b.ogg"))).toBe(true);
    expect(FakeContext.made.length).toBe(1);
    expect(FakeContext.made[0].sources.length).toBe(2);
    expect(FakeContext.made[0].filters.length).toBe(20);
  });

  it("chains source through the ten filters to destination, with the types, frequencies and Q", async () => {
    const g = await load();
    await g.attach(el());
    const c = FakeContext.made[0];
    const chain: FakeNode[] = [c.sources[0], ...c.filters];
    for (let i = 0; i < chain.length - 1; i++) {
      expect(chain[i].out.length).toBe(1);
      expect(chain[i].out[0]).toBe(chain[i + 1]);
    }
    expect(c.filters[9].out.length).toBe(1);
    expect(c.filters[9].out[0]).toBe(c.destination);
    expect(c.filters.map((f) => f.type)).toEqual([
      "lowshelf",
      "peaking",
      "peaking",
      "peaking",
      "peaking",
      "peaking",
      "peaking",
      "peaking",
      "peaking",
      "highshelf",
    ]);
    expect(c.filters.map((f) => f.frequency.value)).toEqual([...BANDS]);
    for (const f of c.filters.slice(1, 9)) expect(f.Q.value).toBe(1.41);
    expect(c.filters.every((f) => f.gain.value === 0)).toBe(true);
  });
});

describe("setGains", () => {
  it("sets each filter's gain, and off means all 0", async () => {
    const g = await load();
    const m = el();
    await g.attach(m);
    const gains = [6, 5, 4, 2, 0, 0, 0, 0, 0, 0];
    g.setGains(m, gains);
    expect(FakeContext.made[0].filters.map((f) => f.gain.value)).toEqual(gains);
    g.setGains(m, BANDS.map(() => 0));
    expect(FakeContext.made[0].filters.map((f) => f.gain.value)).toEqual([0, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
  });

  it("does nothing for an element that was never attached", async () => {
    const g = await load();
    expect(() => g.setGains(el(), [1, 2, 3, 4, 5, 6, 7, 8, 9, 10])).not.toThrow();
    expect(FakeContext.made.length).toBe(0);
  });
});

describe("the context's state", () => {
  it("tries resume once when the context leaves running while an attached element plays", async () => {
    const g = await load();
    await g.attach(el("/a.ogg", false));
    const c = FakeContext.made[0];
    expect(c.resumes).toBe(1);
    c.state = "suspended";
    c.onstatechange?.();
    expect(c.resumes).toBe(2);
    // The try brought the context back to running: a state change to running asks for no try.
    c.onstatechange?.();
    expect(c.resumes).toBe(2);
  });

  it("does not try resume while every attached element is paused", async () => {
    const g = await load();
    await g.attach(el());
    const c = FakeContext.made[0];
    c.state = "suspended";
    c.onstatechange?.();
    expect(c.resumes).toBe(1);
  });
});
