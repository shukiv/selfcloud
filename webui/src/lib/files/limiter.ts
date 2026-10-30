// SPDX-License-Identifier: AGPL-3.0-or-later

// Limiter runs at most max tasks at once. A task that waits takes its turn in order.
export class Limiter {
  private active = 0;
  private waiting: Array<() => void> = [];

  constructor(private max: number) {
    if (!(max >= 1)) throw new Error('a limiter needs at least one slot');
  }

  run<T>(task: () => Promise<T>): Promise<T> {
    return this.acquire().then(async () => {
      try {
        return await task();
      } finally {
        this.release();
      }
    });
  }

  get running(): number {
    return this.active;
  }

  private acquire(): Promise<void> {
    if (this.active < this.max) {
      this.active++;
      return Promise.resolve();
    }
    return new Promise<void>((resolve) => {
      this.waiting.push(() => {
        this.active++;
        resolve();
      });
    });
  }

  private release(): void {
    this.active--;
    const next = this.waiting.shift();
    if (next) next();
  }
}
