"use client";

import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

import type { Kind, Operation } from "@/api/client";

import { OperationSheet } from "./operation-sheet";

type Target = { mode: "new"; kind: Kind } | { mode: "edit"; operation: Operation };

type Value = {
  openNew: (kind?: Kind) => void;
  openEdit: (operation: Operation) => void;
  /** Grows by one after each saved operation: Savik reacts to it. */
  saves: number;
};

const OperationSheetContext = createContext<Value | null>(null);

/** One add/edit sheet for the whole app: the "+" button and any list open it. */
export function OperationSheetProvider({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<Target | null>(null);
  const [saves, setSaves] = useState(0);
  const openNew = useCallback((kind: Kind = "expense") => setTarget({ mode: "new", kind }), []);
  const openEdit = useCallback(
    (operation: Operation) => setTarget({ mode: "edit", operation }),
    [],
  );
  const value = useMemo(() => ({ openNew, openEdit, saves }), [openNew, openEdit, saves]);

  return (
    <OperationSheetContext.Provider value={value}>
      {children}
      <OperationSheet
        target={target}
        onClose={() => setTarget(null)}
        onSaved={() => setSaves((n) => n + 1)}
      />
    </OperationSheetContext.Provider>
  );
}

export function useOperationSheet(): Value {
  const v = useContext(OperationSheetContext);
  if (!v) throw new Error("useOperationSheet outside OperationSheetProvider");
  return v;
}

export type { Target as SheetTarget };
