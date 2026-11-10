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
  // The editor page (decision 124): the save and wrap buttons' names, the save states, and the notices of a save that did not
  // happen. The page shows these words, never the server's.
  save: string;
  saving: string;
  wrap: string;
  reload: string;
  reloadConfirm: string;
  retry: string;
  conflict: string;
  locked: string;
  forbidden: string;
  gone: string;
  notText: string;
  tooLarge: string;
  failed: string;
  tooLargeToEdit: string;
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
    save: "Save",
    saving: "Saving…",
    wrap: "Wrap lines",
    reload: "Reload",
    reloadConfirm: "Unsaved changes will be lost. Reload the saved version?",
    retry: "Try again",
    conflict: "This file changed since you opened it. Reload it to see the saved version.",
    locked: "This file is open in an office editor. Try again later.",
    forbidden: "You can no longer save this file. The text stays here so you can copy it.",
    gone: "This file was deleted. The text stays here so you can copy it.",
    notText: "The text is not UTF-8, so it was not saved. The text stays here.",
    tooLarge: "The file is too large to save here.",
    failed: "Save failed. The text stays here, so try again.",
    tooLargeToEdit: "This file is too large to edit here. You can download it.",
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
    save: "Guardar",
    saving: "Guardando…",
    wrap: "Ajustar líneas",
    reload: "Recargar",
    reloadConfirm: "Se perderán los cambios sin guardar. ¿Recargar la versión guardada?",
    retry: "Reintentar",
    conflict: "Este archivo cambió desde que lo abrió. Recárguelo para ver la versión guardada.",
    locked: "Este archivo está abierto en un editor de oficina. Inténtelo más tarde.",
    forbidden: "Ya no puede guardar este archivo. El texto se queda aquí para que pueda copiarlo.",
    gone: "Este archivo se eliminó. El texto se queda aquí para que pueda copiarlo.",
    notText: "El texto no es UTF-8, así que no se guardó. El texto se queda aquí.",
    tooLarge: "El archivo es demasiado grande para guardarlo aquí.",
    failed: "No se pudo guardar. El texto se queda aquí; inténtelo de nuevo.",
    tooLargeToEdit: "Este archivo es demasiado grande para editarlo aquí. Puede descargarlo.",
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
    save: "Enregistrer",
    saving: "Enregistrement…",
    wrap: "Retour à la ligne",
    reload: "Recharger",
    reloadConfirm: "Les modifications non enregistrées seront perdues. Recharger la version enregistrée ?",
    retry: "Réessayer",
    conflict: "Ce fichier a changé depuis son ouverture. Rechargez-le pour voir la version enregistrée.",
    locked: "Ce fichier est ouvert dans un éditeur de bureautique. Réessayez plus tard.",
    forbidden: "Vous ne pouvez plus enregistrer ce fichier. Le texte reste ici pour que vous puissiez le copier.",
    gone: "Ce fichier a été supprimé. Le texte reste ici pour que vous puissiez le copier.",
    notText: "Le texte n’est pas en UTF-8, il n’a donc pas été enregistré. Le texte reste ici.",
    tooLarge: "Le fichier est trop volumineux pour être enregistré ici.",
    failed: "L’enregistrement a échoué. Le texte reste ici ; réessayez.",
    tooLargeToEdit: "Ce fichier est trop volumineux pour être modifié ici. Vous pouvez le télécharger.",
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
    save: "Guardar",
    saving: "A guardar…",
    wrap: "Quebrar linhas",
    reload: "Recarregar",
    reloadConfirm: "As alterações não guardadas serão perdidas. Recarregar a versão guardada?",
    retry: "Tentar de novo",
    conflict: "Este ficheiro mudou desde que o abriu. Recarregue-o para ver a versão guardada.",
    locked: "Este ficheiro está aberto num editor de escritório. Tente mais tarde.",
    forbidden: "Já não pode guardar este ficheiro. O texto fica aqui para o poder copiar.",
    gone: "Este ficheiro foi eliminado. O texto fica aqui para o poder copiar.",
    notText: "O texto não está em UTF-8, por isso não foi guardado. O texto fica aqui.",
    tooLarge: "O ficheiro é grande demais para ser guardado aqui.",
    failed: "Não foi possível guardar. O texto fica aqui; tente de novo.",
    tooLargeToEdit: "Este ficheiro é grande demais para ser editado aqui. Pode transferi-lo.",
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
    save: "Сохранить",
    saving: "Сохранение…",
    wrap: "Переносить строки",
    reload: "Перезагрузить",
    reloadConfirm: "Несохранённые изменения будут потеряны. Перезагрузить сохранённую версию?",
    retry: "Повторить",
    conflict: "Файл изменился после того, как вы его открыли. Перезагрузите его, чтобы увидеть сохранённую версию.",
    locked: "Файл открыт в редакторе документов. Повторите позже.",
    forbidden: "Вы больше не можете сохранить этот файл. Текст остаётся здесь, чтобы его можно было скопировать.",
    gone: "Файл удалён. Текст остаётся здесь, чтобы его можно было скопировать.",
    notText: "Текст не в кодировке UTF-8, поэтому он не сохранён. Текст остаётся здесь.",
    tooLarge: "Файл слишком большой, чтобы сохранить его здесь.",
    failed: "Не удалось сохранить. Текст остаётся здесь; попробуйте ещё раз.",
    tooLargeToEdit: "Файл слишком большой, чтобы редактировать его здесь. Его можно скачать.",
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
    save: "حفظ",
    saving: "جارٍ الحفظ…",
    wrap: "التفاف الأسطر",
    reload: "إعادة التحميل",
    reloadConfirm: "ستفقد التغييرات غير المحفوظة. هل تريد إعادة تحميل النسخة المحفوظة؟",
    retry: "إعادة المحاولة",
    conflict: "تغيّر هذا الملف منذ فتحته. أعد تحميله لترى النسخة المحفوظة.",
    locked: "هذا الملف مفتوح في محرر مستندات. حاول لاحقًا.",
    forbidden: "لم تعد تستطيع حفظ هذا الملف. يبقى النص هنا لتتمكن من نسخه.",
    gone: "تم حذف هذا الملف. يبقى النص هنا لتتمكن من نسخه.",
    notText: "النص ليس بترميز UTF-8، فلم يُحفظ. يبقى النص هنا.",
    tooLarge: "الملف أكبر من أن يُحفظ هنا.",
    failed: "تعذّر الحفظ. يبقى النص هنا؛ حاول مرة أخرى.",
    tooLargeToEdit: "الملف أكبر من أن يُحرَّر هنا. يمكنك تنزيله.",
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
    save: "שמירה",
    saving: "שומר…",
    wrap: "גלישת שורות",
    reload: "טעינה מחדש",
    reloadConfirm: "שינויים שלא נשמרו יאבדו. לטעון מחדש את הגרסה השמורה?",
    retry: "ניסיון נוסף",
    conflict: "הקובץ השתנה מאז שפתחת אותו. טען אותו מחדש כדי לראות את הגרסה השמורה.",
    locked: "הקובץ פתוח בעורך מסמכים. נסה שוב מאוחר יותר.",
    forbidden: "אין לך עוד הרשאה לשמור את הקובץ הזה. הטקסט נשאר כאן כדי שתוכל להעתיק אותו.",
    gone: "הקובץ נמחק. הטקסט נשאר כאן כדי שתוכל להעתיק אותו.",
    notText: "הטקסט אינו בקידוד UTF-8 ולכן לא נשמר. הטקסט נשאר כאן.",
    tooLarge: "הקובץ גדול מכדי לשמור כאן.",
    failed: "השמירה נכשלה. הטקסט נשאר כאן; נסה שוב.",
    tooLargeToEdit: "הקובץ גדול מכדי לערוך כאן. אפשר להוריד אותו.",
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
