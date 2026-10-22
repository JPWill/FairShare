// Live filtering for the Loot and Players search boxes. Typing hides rows on the current
// page that do not contain the text; pressing Enter still submits the form, which searches
// the whole ledger on the server. The page works without this file: it only narrows what
// is already loaded. No requests, no storage.
(function () {
  "use strict";
  var box = document.querySelector("form.search input[type=search]");
  if (!box) return;
  var tables = document.querySelectorAll("main .scroll table");
  if (!tables.length) return;
  var note = document.createElement("p");
  note.className = "note filter-note";
  note.hidden = true;
  box.closest("form").appendChild(note);

  function apply() {
    var q = box.value.trim().toLowerCase();
    var shown = 0, total = 0;
    tables.forEach(function (t) {
      t.querySelectorAll("tbody tr").forEach(function (tr) {
        total++;
        var hit = !q || tr.textContent.toLowerCase().indexOf(q) !== -1;
        tr.hidden = !hit;
        if (hit) shown++;
      });
    });
    if (!q) {
      note.hidden = true;
    } else {
      note.hidden = false;
      note.textContent = shown + " of " + total + " on this page match. Press Enter to search every page.";
    }
  }
  box.addEventListener("input", apply);
  if (box.value) apply();
})();
