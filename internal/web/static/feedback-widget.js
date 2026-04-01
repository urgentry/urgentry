// Gentry Feedback Widget
// Injects a floating feedback button on any page. On submit, POSTs a
// user_report envelope item to /api/{project_id}/envelope/.
//
// Usage:
//   <script src="https://your-gentry/static/feedback-widget.js"
//           data-dsn="https://key@your-gentry/project_id"></script>
//
(function () {
  "use strict";

  // --- Configuration ---------------------------------------------------

  var scriptTag = document.currentScript;
  var dsn = scriptTag && scriptTag.getAttribute("data-dsn");
  var projectID = "";
  var envelopeURL = "";

  if (dsn) {
    // DSN format: https://<key>@<host>/<project_id>
    try {
      var url = new URL(dsn);
      projectID = url.pathname.replace(/^\/+/, "").replace(/\/+$/, "");
      envelopeURL =
        url.protocol + "//" + url.host + "/api/" + projectID + "/envelope/";
    } catch (e) {
      console.warn("[gentry-feedback] Invalid DSN:", dsn);
      return;
    }
  }

  if (!envelopeURL) {
    console.warn(
      "[gentry-feedback] No data-dsn attribute found on script tag."
    );
    return;
  }

  // --- Styles ----------------------------------------------------------

  var STYLES =
    ".gentry-fb-btn{" +
    "position:fixed;bottom:20px;right:20px;z-index:99999;" +
    "background:#7F5AF0;color:#fff;border:none;border-radius:50%;" +
    "width:48px;height:48px;font-size:22px;cursor:pointer;" +
    "box-shadow:0 2px 8px rgba(0,0,0,0.3);transition:background .2s}" +
    ".gentry-fb-btn:hover{background:#6B4AD4}" +
    ".gentry-fb-overlay{" +
    "position:fixed;inset:0;z-index:100000;" +
    "background:rgba(0,0,0,0.5);display:flex;align-items:center;" +
    "justify-content:center}" +
    ".gentry-fb-form{" +
    "background:#242629;color:#E0E0E0;border-radius:8px;" +
    "padding:24px;width:360px;max-width:90vw;" +
    "box-shadow:0 4px 24px rgba(0,0,0,0.5);font-family:sans-serif}" +
    ".gentry-fb-form h3{margin:0 0 16px;font-size:16px;color:#fff}" +
    ".gentry-fb-form label{display:block;font-size:13px;margin-bottom:4px;color:#9CA0B0}" +
    ".gentry-fb-form input,.gentry-fb-form textarea{" +
    "width:100%;box-sizing:border-box;padding:8px 10px;margin-bottom:12px;" +
    "background:#16161A;color:#E0E0E0;border:1px solid #33363F;" +
    "border-radius:4px;font-size:14px;font-family:inherit}" +
    ".gentry-fb-form textarea{resize:vertical;min-height:80px}" +
    ".gentry-fb-actions{display:flex;justify-content:flex-end;gap:8px}" +
    ".gentry-fb-actions button{" +
    "padding:8px 16px;border:none;border-radius:4px;" +
    "font-size:13px;cursor:pointer}" +
    ".gentry-fb-cancel{background:#33363F;color:#9CA0B0}" +
    ".gentry-fb-cancel:hover{background:#3F424B}" +
    ".gentry-fb-submit{background:#7F5AF0;color:#fff}" +
    ".gentry-fb-submit:hover{background:#6B4AD4}" +
    ".gentry-fb-submit:disabled{opacity:0.5;cursor:not-allowed}" +
    ".gentry-fb-msg{font-size:13px;margin-bottom:12px;padding:8px;border-radius:4px}" +
    ".gentry-fb-msg-ok{background:rgba(44,182,125,0.15);color:#2CB67D}" +
    ".gentry-fb-msg-err{background:rgba(255,80,80,0.15);color:#FF5050}";

  var styleEl = document.createElement("style");
  styleEl.textContent = STYLES;
  document.head.appendChild(styleEl);

  // --- DOM Elements ----------------------------------------------------

  var btn = document.createElement("button");
  btn.className = "gentry-fb-btn";
  btn.type = "button";
  btn.title = "Send Feedback";
  btn.textContent = "\u2709"; // envelope emoji
  document.body.appendChild(btn);

  var overlay = null;

  // --- Helpers ---------------------------------------------------------

  function generateEventID() {
    var hex = "0123456789abcdef";
    var id = "";
    for (var i = 0; i < 32; i++) {
      id += hex[(Math.random() * 16) | 0];
    }
    return id;
  }

  function showForm() {
    if (overlay) return;

    overlay = document.createElement("div");
    overlay.className = "gentry-fb-overlay";

    var form = document.createElement("div");
    form.className = "gentry-fb-form";
    form.innerHTML =
      "<h3>Send Feedback</h3>" +
      '<div class="gentry-fb-msg-container"></div>' +
      "<label>Name</label>" +
      '<input type="text" name="name" placeholder="Your name">' +
      "<label>Email</label>" +
      '<input type="email" name="email" placeholder="you@example.com">' +
      "<label>Comments</label>" +
      '<textarea name="comments" placeholder="What happened?"></textarea>' +
      '<div class="gentry-fb-actions">' +
      '<button type="button" class="gentry-fb-cancel">Cancel</button>' +
      '<button type="button" class="gentry-fb-submit">Submit</button>' +
      "</div>";

    overlay.appendChild(form);
    document.body.appendChild(overlay);

    // Close on overlay click (not form click)
    overlay.addEventListener("click", function (e) {
      if (e.target === overlay) hideForm();
    });

    form.querySelector(".gentry-fb-cancel").addEventListener("click", hideForm);
    form.querySelector(".gentry-fb-submit").addEventListener("click", submit);
  }

  function hideForm() {
    if (overlay) {
      overlay.remove();
      overlay = null;
    }
  }

  function submit() {
    if (!overlay) return;
    var form = overlay.querySelector(".gentry-fb-form");
    var name = form.querySelector('[name="name"]').value.trim();
    var email = form.querySelector('[name="email"]').value.trim();
    var comments = form.querySelector('[name="comments"]').value.trim();

    if (!comments) {
      showMessage(form, "Please enter your feedback.", true);
      return;
    }

    var submitBtn = form.querySelector(".gentry-fb-submit");
    submitBtn.disabled = true;

    var eventID = generateEventID();

    // Build a Sentry envelope with a user_report item.
    var envelopeHeader = JSON.stringify({ event_id: eventID });
    var itemHeader = JSON.stringify({
      type: "user_report",
      length: 0, // will be set by payload size
    });
    var payload = JSON.stringify({
      event_id: eventID,
      name: name,
      email: email,
      comments: comments,
    });

    // Sentry envelope wire format: header\nitem_header\npayload\n
    var body = envelopeHeader + "\n" + itemHeader + "\n" + payload + "\n";

    var xhr = new XMLHttpRequest();
    xhr.open("POST", envelopeURL, true);
    xhr.setRequestHeader("Content-Type", "application/x-sentry-envelope");
    xhr.onload = function () {
      if (xhr.status >= 200 && xhr.status < 300) {
        showMessage(form, "Thank you for your feedback!", false);
        setTimeout(hideForm, 1500);
      } else {
        showMessage(form, "Failed to submit feedback. Please try again.", true);
        submitBtn.disabled = false;
      }
    };
    xhr.onerror = function () {
      showMessage(form, "Network error. Please try again.", true);
      submitBtn.disabled = false;
    };
    xhr.send(body);
  }

  function showMessage(form, text, isError) {
    var container = form.querySelector(".gentry-fb-msg-container");
    container.innerHTML =
      '<div class="gentry-fb-msg ' +
      (isError ? "gentry-fb-msg-err" : "gentry-fb-msg-ok") +
      '">' +
      escapeHtml(text) +
      "</div>";
  }

  function escapeHtml(s) {
    var div = document.createElement("div");
    div.textContent = s;
    return div.innerHTML;
  }

  // --- Bind ------------------------------------------------------------

  btn.addEventListener("click", showForm);
})();
