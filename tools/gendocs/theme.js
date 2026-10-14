// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

// Light or dark, as the reader last chose, else as the OS prefers. The choice is stored as the
// API references store theirs (localStorage "colorMode"). Loaded in <head>, so the theme is set
// before the page paints.
(function () {
  "use strict";
  var KEY = "colorMode";
  var root = document.documentElement;
  var os = window.matchMedia("(prefers-color-scheme: dark)");
  // Bootstrap Icons' sun and moon (MIT).
  var ICONS = {
    light:
      "M8 11a3 3 0 1 1 0-6 3 3 0 0 1 0 6m0 1a4 4 0 1 0 0-8 4 4 0 0 0 0 8M8 0a.5.5 0 0 1 .5.5v2a.5.5 0 0 1-1 0v-2A.5.5 0 0 1 8 0m0 13a.5.5 0 0 1 .5.5v2a.5.5 0 0 1-1 0v-2A.5.5 0 0 1 8 13m8-5a.5.5 0 0 1-.5.5h-2a.5.5 0 0 1 0-1h2a.5.5 0 0 1 .5.5M3 8a.5.5 0 0 1-.5.5h-2a.5.5 0 0 1 0-1h2A.5.5 0 0 1 3 8m10.657-5.657a.5.5 0 0 1 0 .707l-1.414 1.415a.5.5 0 1 1-.707-.708l1.414-1.414a.5.5 0 0 1 .707 0m-9.193 9.193a.5.5 0 0 1 0 .707L3.05 13.657a.5.5 0 0 1-.707-.707l1.414-1.414a.5.5 0 0 1 .707 0m9.193 2.121a.5.5 0 0 1-.707 0l-1.414-1.414a.5.5 0 0 1 .707-.707l1.414 1.414a.5.5 0 0 1 0 .707M4.464 4.465a.5.5 0 0 1-.707 0L2.343 3.05a.5.5 0 1 1 .707-.707l1.414 1.414a.5.5 0 0 1 0 .708",
    dark:
      "M6 .278a.77.77 0 0 1 .08.858 7.2 7.2 0 0 0-.878 3.46c0 4.021 3.278 7.277 7.318 7.277q.792-.001 1.533-.16a.79.79 0 0 1 .81.316.73.73 0 0 1-.031.893A8.35 8.35 0 0 1 8.344 16C3.734 16 0 12.286 0 7.71 0 4.266 2.114 1.312 5.124.06A.75.75 0 0 1 6 .278M4.858 1.311A7.27 7.27 0 0 0 1.025 7.71c0 4.02 3.279 7.276 7.319 7.276a7.32 7.32 0 0 0 5.205-2.162q-.506.063-1.029.063c-4.61 0-8.343-3.714-8.343-8.29 0-1.167.242-2.278.681-3.286",
  };
  var SVG = "http://www.w3.org/2000/svg";

  function chosen() {
    try {
      var mode = localStorage.getItem(KEY);
      return mode === "light" || mode === "dark" ? mode : null;
    } catch (e) {
      return null;
    }
  }

  function current() {
    return chosen() || (os.matches ? "dark" : "light");
  }

  function apply() {
    var mode = chosen();
    if (mode) {
      root.setAttribute("data-theme", mode);
    } else {
      root.removeAttribute("data-theme");
    }
  }

  apply();

  document.addEventListener("DOMContentLoaded", function () {
    var header = document.querySelector("body > header");
    if (!header) {
      return;
    }
    var button = document.createElement("button");
    button.type = "button";
    button.className = "theme-toggle";
    var icon = document.createElementNS(SVG, "svg");
    icon.setAttribute("width", "16");
    icon.setAttribute("height", "16");
    icon.setAttribute("viewBox", "0 0 16 16");
    icon.setAttribute("fill", "currentColor");
    icon.setAttribute("aria-hidden", "true");
    var path = document.createElementNS(SVG, "path");
    icon.appendChild(path);
    button.appendChild(icon);
    // Shows the mode a click switches to, as the API references' button does.
    function label() {
      var next = current() === "dark" ? "light" : "dark";
      path.setAttribute("d", ICONS[next]);
      button.title = "Set " + next + " mode";
      button.setAttribute("aria-label", button.title);
    }
    button.addEventListener("click", function () {
      try {
        localStorage.setItem(KEY, current() === "dark" ? "light" : "dark");
      } catch (e) {
        return;
      }
      apply();
      label();
    });
    os.addEventListener("change", label);
    // A choice made in another tab.
    window.addEventListener("storage", function (e) {
      if (e.key === KEY) {
        apply();
        label();
      }
    });
    label();
    header.appendChild(button);
  });
})();
