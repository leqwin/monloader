// Focus the command bar on the add screen so a pasted URL needs no click.
document.addEventListener("DOMContentLoaded", function () {
  var bar = document.getElementById("add-input");
  if (bar) {
    bar.focus();
  }
});

// A job's items render expanded. The 2s poll and an in-place add swap the whole
// rows body, re-emitting each job open and its active "+N more" sub-list closed,
// so remember which jobs the operator collapsed and which "+N more" lists they
// expanded (toggle does not bubble, so capture it) and re-apply both after a
// swap. The collapsed set rides in sessionStorage so a full page refresh, which
// reloads the rows from scratch, keeps the operator's collapsed rows collapsed.
function loadClosedJobs() {
  try {
    return new Set(JSON.parse(sessionStorage.getItem("monloaderClosedJobs") || "[]"));
  } catch (e) {
    return new Set();
  }
}
var closedJobs = loadClosedJobs();
var expandedMore = new Set();
function applyJobStates() {
  closedJobs.forEach(function (id) {
    var d = document.querySelector('#queue-rows details[data-job="' + id + '"]');
    if (d) {
      d.open = false;
    }
  });
  expandedMore.forEach(function (id) {
    var d = document.querySelector('#queue-rows details[data-job="' + id + '"] details.more-items');
    if (d) {
      d.open = true;
    }
  });
}
document.addEventListener("toggle", function (e) {
  var d = e.target;
  if (!d || d.tagName !== "DETAILS") {
    return;
  }
  if (d.dataset.job) {
    if (d.open) {
      closedJobs.delete(d.dataset.job);
    } else {
      closedJobs.add(d.dataset.job);
    }
    try {
      sessionStorage.setItem("monloaderClosedJobs", JSON.stringify(Array.from(closedJobs)));
    } catch (e) {}
  } else if (d.classList.contains("more-items")) {
    var owner = d.closest("details[data-job]");
    if (owner) {
      if (d.open) {
        expandedMore.add(owner.dataset.job);
      } else {
        expandedMore.delete(owner.dataset.job);
      }
    }
  }
}, true);
document.addEventListener("DOMContentLoaded", applyJobStates);
document.body.addEventListener("htmx:afterSwap", function (e) {
  if (e.target && e.target.id === "queue-rows") {
    applyJobStates();
  }
});

// Reflect monbooru connectivity into the add forms. The footer light polls
// /internal/monbooru-status and swaps its live state onto data-conn; when
// monbooru is unreachable, unpaired, or rejecting the token, reveal the top
// banner and block URL submission (a queued download could only fail at the
// push step). The transient "checking" state leaves the server-rendered state
// in place so the banner does not flash.
function applyMonbooruState(conn) {
  if (conn !== "down" && conn !== "ok" && conn !== "rejected" && conn !== "unpaired") {
    return;
  }
  var blocked = conn === "down" || conn === "rejected" || conn === "unpaired";
  var banner = document.getElementById("monbooru-banner");
  if (banner) {
    banner.hidden = !blocked;
    var msg = document.getElementById("monbooru-banner-msg");
    if (msg && blocked) {
      msg.textContent = conn === "rejected" ? "monbooru rejected the api token"
        : conn === "unpaired" ? "monbooru is not paired"
        : "monbooru is unreachable";
    }
  }
  document.querySelectorAll(".needs-monbooru").forEach(function (el) {
    el.disabled = blocked;
  });
}
document.body.addEventListener("htmx:afterSwap", function () {
  var light = document.getElementById("conn-light");
  if (light) {
    applyMonbooruState(light.dataset.conn);
  }
});

// Per-site settings edit: fill the shared pop-in from the row's data and open
// it. The api key is never echoed (only whether one is set); leaving it blank
// keeps the stored value.
document.body.addEventListener("click", function (e) {
  var btn = e.target.closest && e.target.closest(".edit-site");
  if (!btn) {
    return;
  }
  var d = btn.dataset;
  document.getElementById("se-name").value = d.site;
  document.getElementById("se-title").textContent = d.site;
  document.getElementById("se-username").value = d.username || "";
  document.getElementById("se-userid").value = d.userid || "";
  document.getElementById("se-gallery").value = d.gallery || "";
  var cookies = document.getElementById("se-cookies");
  cookies.value = d.cookies || "";
  var dir = document.getElementById("site-edit-dialog").dataset.cookiesDir || "";
  cookies.placeholder = dir ? dir + "/" + d.site + ".txt" : "";
  var key = document.getElementById("se-apikey");
  key.value = "";
  key.placeholder = d.haskey ? "(set - leave blank to keep)" : "(unset)";
  // Show only the credentials this site's auth kind uses: the danbooru/e621
  // family signs in by username, gelbooru by user id; cookies/none sites neither.
  var auth = d.auth || "";
  document.getElementById("se-field-username").style.display = auth === "api_optional" ? "" : "none";
  document.getElementById("se-field-apikey").style.display =
    auth === "api_optional" || auth === "api_required" ? "" : "none";
  document.getElementById("se-field-userid").style.display = auth === "api_required" ? "" : "none";
  document.getElementById("site-edit-dialog").showModal();
});

// Route hx-confirm prompts through an in-page pop-in instead of the browser's
// native confirm(). A destructive action carries data-confirm-danger, which
// reddens the OK button and lands focus on cancel so an accidental Enter does
// not commit it.
function showConfirm(message, onOk, okLabel, danger) {
  var dlg = document.getElementById("confirm-dialog");
  if (!dlg) {
    if (window.confirm(message)) {
      onOk();
    }
    return;
  }
  document.getElementById("confirm-dialog-msg").textContent = message || "";
  var okBtn = document.getElementById("confirm-dialog-ok");
  var cancelBtn = document.getElementById("confirm-dialog-cancel");
  okBtn.textContent = okLabel || "ok";
  okBtn.classList.toggle("btn-danger", !!danger);
  var close = function () {
    dlg.close();
    okBtn.onclick = null;
    cancelBtn.onclick = null;
  };
  okBtn.onclick = function () {
    close();
    onOk();
  };
  cancelBtn.onclick = close;
  dlg.showModal();
  (danger ? cancelBtn : okBtn).focus();
}
document.body.addEventListener("htmx:confirm", function (e) {
  if (!e.detail || !e.detail.question) {
    return;
  }
  e.preventDefault();
  var elt = e.detail.elt;
  var okLabel = elt && elt.dataset ? elt.dataset.confirmOk : "";
  var danger = !!(elt && elt.hasAttribute && elt.hasAttribute("data-confirm-danger"));
  showConfirm(e.detail.question, function () { e.detail.issueRequest(true); }, okLabel, danger);
});

// The per-token privileges dialog closes itself on a successful save; the
// scopes cell and the parent flash arrive as OOB swaps.
document.body.addEventListener("token-saved", function (e) {
  var id = e.detail && e.detail.dialog;
  if (!id) return;
  var dlg = document.getElementById(id);
  if (dlg && dlg.open) dlg.close();
});
