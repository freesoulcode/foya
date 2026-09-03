import { ref } from "vue";
import { api } from "@/lib/api";

const revision = ref(0);
let subscribed = false;

export function useContextRevision() {
  if (!subscribed) {
    subscribed = true;
    void api
      .subscribeContextEvents(() => {
        revision.value += 1;
      })
      .catch(() => {
        subscribed = false;
      });
  }

  return revision;
}
