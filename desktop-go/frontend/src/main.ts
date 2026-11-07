import { createApp } from "vue";
import App from "./App.vue";
import "./style.css";

// Suppress the WebKit default context menu (Back/Reload/Inspect) app-wide; the app shows its own
// context menus on the session tree, and elsewhere a right-click should do nothing.
window.addEventListener("contextmenu", (e) => e.preventDefault());

createApp(App).mount("#app");
