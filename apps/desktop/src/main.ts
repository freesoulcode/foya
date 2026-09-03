import { createApp } from "vue";
import "./style.css";
import App from "./App.vue";
import { initializeTheme } from "./composables/useTheme";
import { router } from "./router";

initializeTheme();
createApp(App).use(router).mount("#app");
