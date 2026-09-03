import { computed } from "vue";
import { useKernel } from "@/composables/useKernel";

export function useCurrentProjectId() {
  const { activeSession, draft, isDraft } = useKernel();

  return computed(() =>
    isDraft.value ? draft.projectID : activeSession.value?.project_id ?? ""
  );
}
