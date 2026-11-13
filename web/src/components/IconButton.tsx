// SPDX-License-Identifier: AGPL-3.0-or-later

import { forwardRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from 'react';
import { Button, Tooltip, theme as antdTheme, type ButtonProps } from 'antd';

export type IconButtonProps = Omit<ButtonProps, 'icon' | 'children' | 'shape' | 'type' | 'danger' | 'disabled' | 'title'> & {
  // label is the accessible name, and the tooltip's text unless tip is given.
  label: string;
  // tip is the tooltip's text when it differs from the name (a row's star says "Star", its name says which file).
  tip?: string;
  icon: ReactNode;
  pressed?: boolean;
  disabled?: boolean;
  // danger colours the glyph in the error colour. It draws no box.
  danger?: boolean;
  // popupHost is the tooltip's container, so it shows in full screen (the player's stage).
  popupHost?: () => HTMLElement;
  style?: CSSProperties;
};

// lastInputWasKey is true when the person's last input on the page was a key press, false after a pointer press. A button that
// takes the focus after a key press (Tab, or a menu's Enter) shows its tooltip; one that takes it after a click does not. The
// decision is made here, not by :focus-visible, so it is the same in every engine and can be tested (decision 68).
let lastInputWasKey = false;
if (typeof document !== 'undefined') {
  document.addEventListener('keydown', () => { lastInputWasKey = true; }, true);
  document.addEventListener('pointerdown', () => { lastInputWasKey = false; }, true);
}

// IconButton is the one icon-only button of the interface (decision 68): a lucide icon in an antd text button, round, with no
// border. It is 40 px, and 44 px on a coarse pointer (index.css). Its name is aria-label, and its tooltip is the same text.
// The tooltip shows on a mouse's hover and on keyboard focus, and not after a click: a press opens a menu, and a tooltip over
// the menu would hide its first item. A touch never opens it, so a tap does not flash it. It closes when the pointer leaves or
// the focus does. A disabled button has a tooltip too: the wrapper span, which is always rendered, takes the pointer events
// (index.css makes a disabled icon button pass them through), and a button that turns disabled keeps its place. It does not
// keep its focus: in Chrome the focus moves to the page's body (jsdom keeps it, so a unit test cannot see this). A button
// that must keep the focus while it waits is made aria-disabled instead, as Listing's row icons are (decision 114, which
// corrects decision 68 on this).
const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { label, tip, icon, pressed, disabled, danger, popupHost, className, style, onClick, 'aria-pressed': ariaPressed, ...rest },
  ref,
) {
  const { token } = antdTheme.useToken();
  const [hover, setHover] = useState(false);
  const [keyboard, setKeyboard] = useState(false);
  const [pressedNow, setPressedNow] = useState(false);
  // armed says the tooltip has been wanted once: from then on it is mounted. Until then no Tooltip is mounted, which is the cost
  // each row of a listing paid for its icons (decision 128). The Tooltip anchors to an empty span beside the button, so the
  // button is never moved: a moved button loses its focus and the references held to it.
  const [armed, setArmed] = useState(false);
  const classes = ['icon-btn', pressed ? 'icon-btn--pressed' : '', className ?? ''].filter(Boolean).join(' ');
  const look: CSSProperties = {
    ...(danger ? { color: token.colorError } : {}),
    ...(pressed ? { color: token.colorPrimary, background: token.colorPrimaryBg } : {}),
    ...style,
  };
  const button = (
    <Button
      {...rest}
      ref={ref}
      type="text"
      shape="circle"
      className={classes}
      aria-label={label}
      aria-pressed={pressed ?? ariaPressed}
      disabled={disabled}
      icon={icon}
      style={look}
      onClick={(e: MouseEvent<HTMLElement>) => {
        setPressedNow(true);
        onClick?.(e as MouseEvent<HTMLButtonElement>);
      }}
    />
  );
  const open = !pressedNow && (hover || keyboard);
  return (
    <span
      className="icon-btn-wrap"
      onPointerDown={() => setArmed(true)}
      onPointerEnter={(e) => {
        if (e.pointerType !== 'mouse') return;
        setArmed(true);
        setHover(true);
        setPressedNow(false);
      }}
      onPointerLeave={(e) => {
        if (e.pointerType !== 'mouse') return;
        setHover(false);
        setPressedNow(false);
      }}
      onFocus={() => {
        setArmed(true);
        setKeyboard(lastInputWasKey);
      }}
      onBlur={() => {
        setKeyboard(false);
        setPressedNow(false);
      }}
    >
      {button}
      {armed && (
        <Tooltip title={tip ?? label} open={open} getPopupContainer={popupHost}>
          <span className="icon-btn-anchor" aria-hidden="true" />
        </Tooltip>
      )}
    </span>
  );
});

export default IconButton;
