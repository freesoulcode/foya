import { createApp } from "vue";
import "./style.css";
import App from "./App.vue";
import { initializeTheme } from "./composables/useTheme";

initializeTheme();
createApp(App).mount("#app");
