// SPDX-License-Identifier: AGPL-3.0-or-later

// The code viewer's own strings, in the seven languages (decision 115). They are TypeScript, not a JSON catalogue: they load
// with the preview and the viewer, whose chunks are not named after a language, so check-dist does not count them against a
// language's budget, and the main catalogues do not grow (design-ce 3.3). text.test.ts checks every language has every string.
import type { Lang } from "../i18n";

export interface CodeText {
  // The find panel, through CodeMirror's phrases.
  find: string;
  next: string;
  previous: string;
  all: string;
  matchCase: string;
  regexp: string;
  byWord: string;
  close: string;
  currentMatch: string;
  onLine: string;
  goToLine: string;
  go: string;
  // The empty file's state.
  empty: string;
  // The notice over a file that is not valid UTF-8 (design-ce 2.3). The character is the one shown for each bad sequence.
  notUtf8: string;
}

export const CODE_TEXT: Readonly<Record<Lang, CodeText>> = {
  en: {
    find: "Find",
    next: "next",
    previous: "previous",
    all: "all",
    matchCase: "match case",
    regexp: "regexp",
    byWord: "by word",
    close: "close",
    currentMatch: "current match",
    onLine: "on line",
    goToLine: "Go to line",
    go: "go",
    empty: "This file is empty.",
    notUtf8: "This file is not UTF-8 text. Characters that could not be read show as �.",
  },
  es: {
    find: "Buscar",
    next: "siguiente",
    previous: "anterior",
    all: "todas",
    matchCase: "mayúsculas y minúsculas",
    regexp: "expresión regular",
    byWord: "palabra completa",
    close: "cerrar",
    currentMatch: "coincidencia actual",
    onLine: "en la línea",
    goToLine: "Ir a la línea",
    go: "ir",
    empty: "Este archivo está vacío.",
    notUtf8: "Este archivo no es texto UTF-8. Los caracteres que no se pudieron leer se muestran como �.",
  },
  fr: {
    find: "Rechercher",
    next: "suivant",
    previous: "précédent",
    all: "tout",
    matchCase: "respecter la casse",
    regexp: "expression régulière",
    byWord: "mot entier",
    close: "fermer",
    currentMatch: "correspondance actuelle",
    onLine: "à la ligne",
    goToLine: "Aller à la ligne",
    go: "aller",
    empty: "Ce fichier est vide.",
    notUtf8: "Ce fichier n’est pas du texte UTF-8. Les caractères qui n’ont pas pu être lus s’affichent sous la forme �.",
  },
  pt: {
    find: "Localizar",
    next: "seguinte",
    previous: "anterior",
    all: "todas",
    matchCase: "maiúsculas e minúsculas",
    regexp: "expressão regular",
    byWord: "palavra inteira",
    close: "fechar",
    currentMatch: "correspondência atual",
    onLine: "na linha",
    goToLine: "Ir para a linha",
    go: "ir",
    empty: "Este ficheiro está vazio.",
    notUtf8: "Este ficheiro não é texto UTF-8. Os caracteres que não foi possível ler aparecem como �.",
  },
  ru: {
    find: "Найти",
    next: "далее",
    previous: "назад",
    all: "все",
    matchCase: "с учётом регистра",
    regexp: "регулярное выражение",
    byWord: "слово целиком",
    close: "закрыть",
    currentMatch: "текущее совпадение",
    onLine: "в строке",
    goToLine: "Перейти к строке",
    go: "перейти",
    empty: "Этот файл пуст.",
    notUtf8: "Этот файл не в кодировке UTF-8. Символы, которые не удалось прочитать, показаны как �.",
  },
  ar: {
    find: "بحث",
    next: "التالي",
    previous: "السابق",
    all: "الكل",
    matchCase: "مطابقة حالة الأحرف",
    regexp: "تعبير نمطي",
    byWord: "كلمة كاملة",
    close: "إغلاق",
    currentMatch: "التطابق الحالي",
    onLine: "في السطر",
    goToLine: "الانتقال إلى السطر",
    go: "انتقال",
    empty: "هذا الملف فارغ.",
    notUtf8: "هذا الملف ليس نصًا بترميز UTF-8. تظهر الأحرف التي تعذّرت قراءتها بالشكل �.",
  },
  he: {
    find: "חיפוש",
    next: "הבא",
    previous: "הקודם",
    all: "הכול",
    matchCase: "התאמת רישיות",
    regexp: "ביטוי רגולרי",
    byWord: "מילה שלמה",
    close: "סגירה",
    currentMatch: "ההתאמה הנוכחית",
    onLine: "בשורה",
    goToLine: "מעבר לשורה",
    go: "מעבר",
    empty: "הקובץ הזה ריק.",
    notUtf8: "הקובץ הזה אינו טקסט בקידוד UTF-8. תווים שלא ניתן היה לקרוא מוצגים כ־�.",
  },
};

// codeText is the strings of a language.
export function codeText(l: Lang): CodeText {
  return CODE_TEXT[l] ?? CODE_TEXT.en;
}

// phrasesOf maps CodeMirror's English phrases (the ones the read-only find panel and go-to-line dialog use) to a language's.
export function phrasesOf(t: CodeText): Record<string, string> {
  return {
    Find: t.find,
    next: t.next,
    previous: t.previous,
    all: t.all,
    "match case": t.matchCase,
    regexp: t.regexp,
    "by word": t.byWord,
    close: t.close,
    "current match": t.currentMatch,
    "on line": t.onLine,
    "Go to line": t.goToLine,
    go: t.go,
  };
}
