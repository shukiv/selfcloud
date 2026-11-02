// SPDX-License-Identifier: AGPL-3.0-or-later

// The offline page's words, in the languages the interface has (decision 65). The page is cached by the service worker and
// cannot load the interface's catalogues, so its few strings live here. Each is in the language's own script.
(function () {
  var STRINGS = {
    en: { title: "You are offline", body: "Check the connection, then try again.", retry: "Retry" },
    es: { title: "Sin conexión", body: "Revisa la conexión y vuelve a intentarlo.", retry: "Reintentar" },
    fr: { title: "Vous êtes hors ligne", body: "Vérifiez la connexion, puis réessayez.", retry: "Réessayer" },
    pt: { title: "Está offline", body: "Verifique a ligação e tente novamente.", retry: "Tentar novamente" },
    ru: { title: "Нет подключения", body: "Проверьте соединение и попробуйте ещё раз.", retry: "Повторить" },
    he: { title: "אין חיבור", body: "בדקו את החיבור ונסו שוב.", retry: "נסו שוב" },
    ar: { title: "أنت غير متصل", body: "تحقق من الاتصال ثم حاول مرة أخرى.", retry: "إعادة المحاولة" },
  };

  // The language is the one the person chose in the app, else the browser's, else English.
  var lang = null;
  try {
    lang = window.localStorage.getItem("selfcloud.lang");
  } catch (err) {
    lang = null;
  }
  lang = String(lang || navigator.language || "en").toLowerCase().split("-")[0];
  if (!Object.prototype.hasOwnProperty.call(STRINGS, lang)) lang = "en";

  var words = STRINGS[lang];
  document.documentElement.lang = lang;
  document.documentElement.dir = lang === "he" || lang === "ar" ? "rtl" : "ltr";
  document.getElementById("title").textContent = words.title;
  document.getElementById("body").textContent = words.body;
  document.getElementById("retry").textContent = words.retry;
})();
