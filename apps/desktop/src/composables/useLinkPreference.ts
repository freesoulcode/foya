import { ref } from "vue";

export type LinkOpenMode = "workbar" | "system";

const STORAGE_KEY = "foya-link-open-mode-v1";

function storedMode(): LinkOpenMode {
  return localStorage.getItem(STORAGE_KEY) === "system" ? "system" : "workbar";
}

const linkOpenMode = ref<LinkOpenMode>(storedMode());

export function useLinkPreference() {
  function setLinkOpenMode(mode: LinkOpenMode) {
    linkOpenMode.value = mode;
    localStorage.setItem(STORAGE_KEY, mode);
  }

  return {
    linkOpenMode,
    setLinkOpenMode,
  };
}
