import { createApp } from "vue";
import App from "./App.vue";
import "./style.css";
import { applySkin } from "./themes";

// Suppress the WebKit default context menu (Back/Reload/Inspect) app-wide; the app shows its own
// context menus on the session tree, and elsewhere a right-click should do nothing.
window.addEventListener("contextmenu", (e) => e.preventDefault());

applySkin("system"); // until Settings load (App.vue applies the Skin)
createApp(App).mount("#app");
