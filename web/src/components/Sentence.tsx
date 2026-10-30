// SPDX-License-Identifier: AGPL-3.0-or-later

import { Fragment } from "react";
import { splitParts } from "../lib/signin";

// Sentence fills a translated sentence with names. A name is wrapped in <bdi> so that an address or
// a folder name keeps its own direction inside an Arabic or Hebrew sentence.
export default function Sentence({ template, names }: { template: string; names: Record<string, string> }) {
  return (
    <>
      {splitParts(template, names).map((part, i) =>
        part.name ? <bdi key={i}>{part.text}</bdi> : <Fragment key={i}>{part.text}</Fragment>,
      )}
    </>
  );
}
