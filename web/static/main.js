// Focus the command bar on the add screen so a pasted URL needs no click.
document.addEventListener("DOMContentLoaded", function () {
  var bar = document.getElementById("add-input");
  if (bar) {
    bar.focus();
  }
});

// A job's items start expanded. The queue polls every 2s and swaps the whole
// rows body, which re-emits each job open, so remember which jobs the operator
// collapsed (toggle does not bubble, so capture it) and re-collapse them after
// each swap.
var closedJobs = new Set();
document.addEventListener("toggle", function (e) {
  var d = e.target;
  if (!d || d.tagName !== "DETAILS" || !d.dataset.job) {
    return;
  }
  if (d.open) {
    closedJobs.delete(d.dataset.job);
  } else {
    closedJobs.add(d.dataset.job);
  }
}, true);
document.body.addEventListener("htmx:afterSwap", function (e) {
  if (!e.target || e.target.id !== "queue-rows") {
    return;
  }
  closedJobs.forEach(function (id) {
    var d = document.querySelector('#queue-rows details[data-job="' + id + '"]');
    if (d) {
      d.open = false;
    }
  });
});

// Reflect monbooru connectivity into the add forms. The footer light polls
// /internal/monbooru-status and swaps its live state onto data-conn; when
// monbooru is unreachable or rejecting the token, reveal the top banner and
// block URL submission (a queued download could only fail at the push step).
// The transient "checking" state leaves the server-rendered state in place so
// the banner does not flash.
function applyMonbooruState(conn) {
  if (conn !== "down" && conn !== "ok" && conn !== "rejected") {
    return;
  }
  var blocked = conn === "down" || conn === "rejected";
  var banner = document.getElementById("monbooru-banner");
  if (banner) {
    banner.hidden = !blocked;
    var msg = document.getElementById("monbooru-banner-msg");
    if (msg && blocked) {
      msg.textContent = conn === "rejected"
        ? "monbooru rejected the api token"
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
  document.getElementById("site-edit-dialog").showModal();
});
