import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { useStudioStore } from "@/stores/studio";

const mocks = vi.hoisted(() => ({
  listCanvases: vi.fn(),
}));

vi.mock("@/lib/api", () => ({
  api: {
    listCanvases: mocks.listCanvases,
  },
}));

describe("studio store", () => {
  beforeEach(() => {
    mocks.listCanvases.mockReset();
    mocks.listCanvases.mockResolvedValue([]);
    setActivePinia(createPinia());
  });

  it("loads explicitly and only once", async () => {
    const store = useStudioStore();
    expect(mocks.listCanvases).not.toHaveBeenCalled();

    store.ensureLoaded();
    store.ensureLoaded();

    await vi.waitFor(() => {
      expect(mocks.listCanvases).toHaveBeenCalledTimes(1);
      expect(store.loading).toBe(false);
    });
  });
});
