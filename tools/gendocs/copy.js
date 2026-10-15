// Copyright Monte Carlo AI, Inc.
// SPDX-License-Identifier: Apache-2.0

// A copy button on each block of commands to run, in the guides and the intro's install steps.
// Without JavaScript, or without the clipboard API, there is no button and the text is still
// selectable.
(function () {
  "use strict";
  if (!navigator.clipboard) {
    return;
  }
  // Bootstrap Icons' clipboard and check (MIT).
  var ICONS = {
    copy: [
      "M4 1.5H3a2 2 0 0 0-2 2V14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V3.5a2 2 0 0 0-2-2h-1v1h1a1 1 0 0 1 1 1V14a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V3.5a1 1 0 0 1 1-1h1z",
      "M9.5 1a.5.5 0 0 1 .5.5v1a.5.5 0 0 1-.5.5h-3a.5.5 0 0 1-.5-.5v-1a.5.5 0 0 1 .5-.5zm-3-1A1.5 1.5 0 0 0 5 1.5v1A1.5 1.5 0 0 0 6.5 4h3A1.5 1.5 0 0 0 11 2.5v-1A1.5 1.5 0 0 0 9.5 0z",
    ],
    copied: [
      "M13.854 3.646a.5.5 0 0 1 0 .708l-7 7a.5.5 0 0 1-.708 0l-3.5-3.5a.5.5 0 1 1 .708-.708L6.5 10.293l6.646-6.647a.5.5 0 0 1 .708 0",
    ],
  };
  var SVG = "http://www.w3.org/2000/svg";

  function draw(button, state) {
    var icon = document.createElementNS(SVG, "svg");
    icon.setAttribute("width", "16");
    icon.setAttribute("height", "16");
    icon.setAttribute("viewBox", "0 0 16 16");
    icon.setAttribute("fill", "currentColor");
    icon.setAttribute("aria-hidden", "true");
    (ICONS[state] || ICONS.copy).forEach(function (d) {
      var path = document.createElementNS(SVG, "path");
      path.setAttribute("d", d);
      icon.appendChild(path);
    });
    button.replaceChildren(icon);
    button.title = { copy: "Copy", copied: "Copied", selected: "Selected: press Ctrl+C or ⌘C" }[state];
    button.setAttribute("aria-label", button.title);
  }

  document.querySelectorAll(".runnable pre").forEach(function (pre) {
    // The button sits outside the block, so it stays put while a long command scrolls.
    var wrap = document.createElement("div");
    wrap.className = "copyable";
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);
    var button = document.createElement("button");
    button.type = "button";
    button.className = "copy-button";
    draw(button, "copy");
    function show(state) {
      draw(button, state);
      setTimeout(function () {
        draw(button, "copy");
      }, 1500);
    }
    button.addEventListener("click", function () {
      navigator.clipboard.writeText(pre.textContent).then(
        function () {
          show("copied");
        },
        // Clipboard access refused: select the text, so the reader can copy it by hand.
        function () {
          var range = document.createRange();
          range.selectNodeContents(pre);
          var selection = window.getSelection();
          selection.removeAllRanges();
          selection.addRange(range);
          show("selected");
        },
      );
    });
    wrap.appendChild(button);
  });
})();
