import { ref } from "vue";
import { defineStore } from "pinia";

export const useConnectionStore = defineStore("connection", () => {
  const ready = ref(false);
  const connecting = ref(false);
  const connectError = ref("");

  function beginConnecting() {
    connecting.value = true;
    connectError.value = "";
  }

  function markReady() {
    ready.value = true;
  }

  function fail(error: unknown) {
    connectError.value = String(error);
  }

  function finishConnecting() {
    connecting.value = false;
  }

  return {
    ready,
    connecting,
    connectError,
    beginConnecting,
    markReady,
    fail,
    finishConnecting,
  };
});
