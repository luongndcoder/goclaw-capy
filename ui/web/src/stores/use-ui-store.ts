import { create } from "zustand";
import { persist } from "zustand/middleware";
import i18n from "@/i18n";
import { type Language } from "@/lib/constants";

export type Theme = "light" | "dark" | "system";

interface UiState {
  theme: Theme;
  language: Language;
  timezone: string; // IANA timezone or "auto"
  sidebarCollapsed: boolean;
  mobileSidebarOpen: boolean;
  pageSize: number; // global pagination page size preference
  // Sidebar nav groups the user collapsed. Keys: core, conversations,
  // connectivity, capabilities, data, monitoring, system.
  collapsedGroups: Record<string, boolean>;

  setTheme: (theme: Theme) => void;
  setLanguage: (language: Language) => void;
  setTimezone: (tz: string) => void;
  toggleSidebar: () => void;
  setSidebarCollapsed: (collapsed: boolean) => void;
  setMobileSidebarOpen: (open: boolean) => void;
  setPageSize: (size: number) => void;
  toggleSidebarGroup: (group: string) => void;
}

// Groups hidden by default to keep the sidebar focused on daily work
// (chat/agents). The user can expand them; the choice persists.
const DEFAULT_COLLAPSED_GROUPS: Record<string, boolean> = {
  connectivity: true,
  capabilities: true,
  data: true,
  monitoring: true,
  system: true,
};

export const useUiStore = create<UiState>()(
  persist(
    (set, get) => ({
      theme: "dark" as Theme,
      language: (i18n.language as Language) ?? "en",
      timezone: "auto",
      sidebarCollapsed: false,
      mobileSidebarOpen: false,
      pageSize: 20,
      collapsedGroups: { ...DEFAULT_COLLAPSED_GROUPS },

      setTheme: (theme) => {
        set({ theme });
      },

      setLanguage: (language) => {
        i18n.changeLanguage(language);
        set({ language });
      },

      setTimezone: (tz) => {
        set({ timezone: tz });
      },

      toggleSidebar: () => {
        set({ sidebarCollapsed: !get().sidebarCollapsed });
      },

      setSidebarCollapsed: (collapsed) => {
        set({ sidebarCollapsed: collapsed });
      },

      setMobileSidebarOpen: (open) => set({ mobileSidebarOpen: open }),

      setPageSize: (size) => set({ pageSize: size }),

      toggleSidebarGroup: (group) => {
        const collapsedGroups = {
          ...DEFAULT_COLLAPSED_GROUPS,
          ...get().collapsedGroups,
        };
        collapsedGroups[group] = !collapsedGroups[group];
        set({ collapsedGroups });
      },
    }),
    {
      name: "goclaw:ui", // localStorage key
      partialize: (state) => ({
        // Persist user preferences — not transient UI state
        theme: state.theme,
        language: state.language,
        timezone: state.timezone,
        sidebarCollapsed: state.sidebarCollapsed,
        pageSize: state.pageSize,
        collapsedGroups: state.collapsedGroups,
      }),
    }
  )
);
