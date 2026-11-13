// SPDX-License-Identifier: AGPL-3.0-or-later

// The wait page of a new document (decision 102). The opener of this tab made the file and hands the result over
// under this page's key. The page takes that result and replaces its address with the editor's. It makes no file: a key nobody
// started, or one whose time has passed, shows nothing to open.
import { Alert, Button, Spin } from 'antd';
import { useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useT } from '../lib/i18n';
import { editorPath } from '../lib/files/editor';
import { KEY_PATTERN, listenHandoff, takeHandoff } from '../lib/files/newDocumentHandoff';

type View = { state: 'waiting' } | { state: 'nothing' } | { state: 'error'; text: string };

export default function NewDocumentWaitPage() {
  const [params] = useSearchParams();
  const k = params.get('k') ?? '';
  const navigate = useNavigate();
  // The effect below runs once per key: a new navigate function must not take a result again.
  const navRef = useRef(navigate);
  navRef.current = navigate;
  const { t } = useT();
  const [view, setView] = useState<View>({ state: 'waiting' });

  useEffect(() => {
    if (!KEY_PATTERN.test(k)) {
      setView({ state: 'nothing' });
      return;
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    const check = () => {
      clearTimeout(timer);
      const taken = takeHandoff(k);
      if (taken.state === 'pending') {
        timer = setTimeout(check, Math.max(0, taken.expires - Date.now()) + 1);
        return;
      }
      if (taken.state === 'unknown' || taken.state === 'expired') {
        setView({ state: 'nothing' });
        return;
      }
      const h = taken.handoff;
      if ('error' in h) {
        setView({ state: 'error', text: h.error });
        return;
      }
      navRef.current(editorPath(h.node, 'edit', h.from, h.tab), { replace: true });
    };
    // The listener is set before the first check, so a result handed over between the two is not missed.
    const stop = listenHandoff(k, () => check());
    check();
    return () => {
      stop();
      clearTimeout(timer);
    };
  }, [k]);

  // close ends this tab; a tab the script did not open goes back.
  const close = () => {
    window.close();
    setTimeout(() => {
      if (!window.closed) navigate(-1);
    }, 150);
  };

  if (view.state === 'waiting') {
    return (
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 12, minHeight: '100vh', padding: 16 }}>
        <Spin />
        <span>{t('files.editor.creating')}</span>
      </div>
    );
  }
  const text = view.state === 'error' ? view.text : t('files.editor.nothing');
  return (
    <div style={{ maxWidth: 480, margin: '48px auto', padding: 16 }}>
      <Alert
        type="error"
        showIcon
        message={text}
        action={<Button onClick={close} aria-label={t('files.editor.close')}>{t('files.editor.close')}</Button>}
      />
    </div>
  );
}
