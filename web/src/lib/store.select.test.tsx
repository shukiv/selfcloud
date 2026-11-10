// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { useStoreSelect, writable } from './store';

// useStoreSelect is subscribed once: a re-render of the component does not subscribe to the store again (LOW-3, decision 127).
describe('useStoreSelect subscribes once, not on each render', () => {
  it('keeps its subscription across re-renders of the component', () => {
    const store = writable(1);
    const subscribe = vi.spyOn(store, 'subscribe');

    function View({ n }: { n: number }) {
      const doubled = useStoreSelect(store, (x) => x * 2);
      return <p>{`${doubled + n}`}</p>;
    }
    function Host() {
      const [n, setN] = useState(0);
      return (
        <>
          <button onClick={() => setN(n + 1)}>more</button>
          <View n={n} />
        </>
      );
    }
    render(<Host />);
    const before = subscribe.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    expect(screen.getByText('5')).toBeDefined();
    expect(subscribe.mock.calls.length - before).toBe(0);
  });
});
