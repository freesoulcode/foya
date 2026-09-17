import { createApp } from "vue";
import "./style.css";
import App from "./App.vue";
import { i18n, initializeLocale } from "./i18n";
import { initializeTheme } from "./composables/useTheme";
import { router } from "./router";
import { pinia } from "./stores";

initializeTheme();
initializeLocale();
createApp(App).use(pinia).use(router).use(i18n).mount("#app");
