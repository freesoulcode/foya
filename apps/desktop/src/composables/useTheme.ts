import { ref } from "vue";

export type Theme = "light" | "dark";

const THEME_KEY = "foya-theme-v1";

function storedTheme(): Theme {
  return localStorage.getItem(THEME_KEY) === "dark" ? "dark" : "light";
}

const theme = ref<Theme>(storedTheme());

function applyTheme(value: Theme) {
  document.documentElement.classList.toggle("dark", value === "dark");
  document.documentElement.style.colorScheme = value;
}

export function initializeTheme() {
  applyTheme(theme.value);
}

export function useTheme() {
  function setTheme(value: Theme) {
    theme.value = value;
    localStorage.setItem(THEME_KEY, value);
    applyTheme(value);
  }

  return {
    theme,
    setTheme,
  };
}
