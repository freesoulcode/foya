import { createApp } from "vue";
import "./style.css";
import App from "./App.vue";
import { i18n, initializeLocale } from "./i18n";
import { initializeTheme } from "./composables/useTheme";
import { router } from "./router";

initializeTheme();
initializeLocale();
createApp(App).use(router).use(i18n).mount("#app");
