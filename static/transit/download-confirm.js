// Pre-download confirmation for the Metrics Export button.
//
// Progressive enhancement: the Export link is a real <a download>, so with JS
// off (or <dialog> unsupported) the click downloads directly. With JS on, we
// intercept the click and show #export-dialog — a heads-up listing the ZIP
// contents and an estimated size — and only download when the user confirms.
(function () {
  "use strict";

  var trigger = document.querySelector("[data-export-trigger]");
  var dialog = document.getElementById("export-dialog");
  if (!trigger || !dialog || typeof dialog.showModal !== "function") return;

  trigger.addEventListener("click", function (e) {
    e.preventDefault();
    dialog.showModal();
  });

  // Cancel button and backdrop click both dismiss without downloading.
  dialog.addEventListener("click", function (e) {
    if (e.target === dialog || e.target.closest("[data-export-cancel]")) {
      dialog.close();
    }
  });

  // Confirm is a real <a download>; let it navigate, then close the dialog.
  var confirm = dialog.querySelector("[data-export-confirm]");
  if (confirm) {
    confirm.addEventListener("click", function () {
      setTimeout(function () {
        dialog.close();
      }, 100);
    });
  }
})();
