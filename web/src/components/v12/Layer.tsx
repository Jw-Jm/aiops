import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useEffect,
  useId,
  useRef,
  type ReactNode,
  type RefObject,
} from "react";
import { Button, Drawer, Modal } from "antd";

type Layer = { id: string; close: () => void };
type LayerApi = { push: (layer: Layer) => void; pop: (id: string) => void };
const LayerContext = createContext<LayerApi | null>(null);

export function LayerProvider({ children }: { children: ReactNode }) {
  const stack = useRef<Layer[]>([]);
  const push = useCallback((layer: Layer) => {
    stack.current = stack.current.filter((item) => item.id !== layer.id);
    stack.current.push(layer);
  }, []);
  const pop = useCallback((id: string) => {
    stack.current = stack.current.filter((item) => item.id !== id);
  }, []);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      const top = stack.current[stack.current.length - 1];
      if (!top) return;
      event.preventDefault();
      event.stopPropagation();
      top.close();
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, []);
  const api = useMemo(() => ({ push, pop }), [push, pop]);
  return <LayerContext.Provider value={api}>{children}</LayerContext.Provider>;
}

function useLayer(
  open: boolean,
  onClose: () => void,
  containerRef: RefObject<HTMLElement | null>,
) {
  const api = useContext(LayerContext);
  const id = useId();
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    if (!open || !api) return;
    const previous = document.activeElement as HTMLElement | null;
    api.push({ id, close: () => closeRef.current() });
    // The fixed prototype layers are native <dialog> elements, so the first
    // focusable control - the header close button - takes the initial focus.
    // A control that opts in with data-autofocus still wins, and a layer with
    // neither falls back to its first body control.
    const focusInitial = () => {
      const body = containerRef.current;
      const root = body?.closest<HTMLElement>(".ant-modal, .ant-drawer") ?? body;
      const target =
        root?.querySelector<HTMLElement>("[data-autofocus]") ??
        root?.querySelector<HTMLElement>(".ant-modal-close, .ant-drawer-close") ??
        body?.querySelector<HTMLElement>(
          "button,[href],input,select,textarea,[tabindex]:not([tabindex='-1'])",
        );
      target?.focus({ preventScroll: true });
    };
    const timer = window.setTimeout(focusInitial, 0);
    // The overlay only parks focus on its own container (for screen readers)
    // while it finishes mounting, so re-assert the initial control when focus
    // is outside this layer or still sits on the container itself. That keeps
    // the prototype's "close button focused" state instead of a lost focus.
    const settle = window.setTimeout(() => {
      const body = containerRef.current;
      const root = body?.closest<HTMLElement>(".ant-modal, .ant-drawer") ?? body;
      if (!root) return;
      const active = document.activeElement;
      if (!root.contains(active) || active === root) focusInitial();
    }, 120);
    return () => {
      window.clearTimeout(timer);
      window.clearTimeout(settle);
      api.pop(id);
      previous?.focus?.();
    };
  }, [open, api, id, containerRef]);
}

export function V12Modal({
  open,
  title,
  onClose,
  children,
  footer,
  width = 660,
  rootClassName,
  centered = true,
  closeDisabled = false,
}: {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  width?: number;
  rootClassName?: string;
  centered?: boolean;
  closeDisabled?: boolean;
}) {
  const box = useRef<HTMLDivElement>(null);
  useLayer(open, onClose, box);
  return (
    <Modal
      open={open}
      title={title}
      onCancel={onClose}
      footer={footer ?? null}
      keyboard={false}
      mask={{ closable: false }}
      width={width}
      centered={centered}
      closable={{ disabled: closeDisabled }}
      rootClassName={["v12-layer-root", rootClassName]
        .filter(Boolean)
        .join(" ")}
      destroyOnHidden
    >
      <div ref={box}>{children}</div>
    </Modal>
  );
}

export function V12Drawer({
  open,
  title,
  onClose,
  children,
  footer,
  width = 540,
}: {
  open: boolean;
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  width?: number;
}) {
  const box = useRef<HTMLDivElement>(null);
  useLayer(open, onClose, box);
  return (
    <Drawer
      open={open}
      title={title}
      onClose={onClose}
      keyboard={false}
      mask={{ closable: false }}
      width={width}
      rootClassName="v12-layer-root"
      destroyOnHidden
      footer={footer}
    >
      <div ref={box}>{children}</div>
    </Drawer>
  );
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = "确认",
  cancelLabel = "取消",
  danger = false,
  loading = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  description: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <V12Modal
      open={open}
      title={title}
      onClose={onCancel}
      footer={
        <div className="v12-layer-footer">
          <Button onClick={onCancel}>{cancelLabel}</Button>
          <Button
            type="primary"
            danger={danger}
            loading={loading}
            data-autofocus
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </div>
      }
    >
      <div style={{ color: "#505a70", lineHeight: 1.8 }}>{description}</div>
    </V12Modal>
  );
}
