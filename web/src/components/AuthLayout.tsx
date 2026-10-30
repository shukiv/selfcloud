// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { theme as antdTheme } from "antd";
import { useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";
import { flash } from "../lib/session";
import { RADIUS } from "../theme/tokens";
import Alert from "./Alert";
import LangSwitcher from "./LangSwitcher";
import ThemeToggle from "./ThemeToggle";

// The page every signed-out screen shares. From md (768px) up it is split: a left half with the brand
// and a three-line headline, and a right half with the form. Below md only the form shows, with the
// brand above the card. Nothing links out. Layout is in the .auth-* rules of index.css.
export default function AuthLayout({ children }: { children: ReactNode }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const b = useStore(brand);
  const note = useStore(flash);
  const brandMark = (
    <>
      {b.logo_url && <img src={b.logo_url} alt="" width={56} height={56} />}
      <p className="auth-brand-name">
        <bdi>{b.name}</bdi>
      </p>
    </>
  );
  return (
    <>
      <a className="skip-link" href="#main">
        {t("app.skip")}
      </a>
      <main id="main" tabIndex={-1} className="auth-layout" style={{ background: token.colorBgLayout }}>
        <div className="auth-side" style={{ background: token.colorFillQuaternary }}>
          <div className="auth-side-inner">
            <div className="auth-brand">{brandMark}</div>
            <div className="auth-gap" />
            <div className="auth-headline">
              <div style={{ color: token.colorTextHeading }}>{t("auth.headline1")}</div>
              <div style={{ color: token.colorTextHeading }}>{t("auth.headline2")}</div>
              <div style={{ color: token.colorPrimary }}>{t("auth.headline3")}</div>
            </div>
            <div className="auth-tagline" style={{ color: token.colorTextSecondary }}>
              {t("auth.tagline")}
            </div>
          </div>
        </div>
        <div className="auth-form" style={{ background: token.colorBgContainer }}>
          <div className="auth-form-brand auth-brand">{brandMark}</div>
          {note && (
            <div className="auth-width">
              <Alert message={{ key: note }} tone="success" />
            </div>
          )}
          <div
            className="auth-width auth-card"
            style={{
              background: token.colorBgContainer,
              border: `1px solid ${token.colorBorderSecondary}`,
              borderRadius: RADIUS,
            }}
          >
            {children}
          </div>
          <div className="auth-controls">
            <LangSwitcher />
            <ThemeToggle />
          </div>
        </div>
      </main>
    </>
  );
}
