import dagre from "@dagrejs/dagre";
import { theme, Typography } from "antd";
import { select } from "d3-selection";
import { zoom, zoomIdentity, ZoomTransform } from "d3-zoom";
import { useCallback, useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { DomainEntry } from "../../Services/Domains/Models/domains.model";

const { Text } = Typography;

interface Props {
  data: DomainEntry[];
}

/* ------------------------------------------------------------------ */
/*  Internal types                                                     */
/* ------------------------------------------------------------------ */

interface GraphNode {
  id: string;
  label: string;
  displayLabel: string;
  color: string;
  width: number;
  height: number;
}

interface GraphEdge {
  source: string;
  target: string;
}

interface PositionedNode extends GraphNode {
  x: number;
  y: number;
}

interface LayoutEdge {
  points: { x: number; y: number }[];
}

interface Layout {
  nodes: PositionedNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
}

/* ------------------------------------------------------------------ */
/*  Helpers                                                            */
/* ------------------------------------------------------------------ */

const IPV4_RE = /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$/;
const NODE_HEIGHT = 32;
const LABEL_MAX = 34;
const ARROW_SIZE = 8;
const FONT = "11px system-ui, -apple-system, sans-serif";

function getGroupKey(host: string): string {
  if (IPV4_RE.test(host)) {
    const octets = host.split(".");
    return `${octets[0]}.${octets[1]}.${octets[2]}.0/24`;
  }
  const parts = host.split(".");
  if (parts.length <= 2) return host;
  return parts.slice(-2).join(".");
}

function truncate(text: string, max: number): string {
  return text.length > max ? text.slice(0, max - 1) + "…" : text;
}

/* ------------------------------------------------------------------ */
/*  Graph data construction                                            */
/* ------------------------------------------------------------------ */

function buildGraphData(entries: DomainEntry[], primaryColor: string) {
  const nodeMap = new Map<string, GraphNode>();
  const edges: GraphEdge[] = [];
  const edgeSet = new Set<string>();

  const addNode = (
    id: string,
    label: string,
    color: string,
  ) => {
    if (nodeMap.has(id)) return;
    const displayLabel = truncate(label, LABEL_MAX);
    nodeMap.set(id, {
      id,
      label,
      displayLabel,
      color,
      width: Math.max(displayLabel.length * 7.5 + 24, 100),
      height: NODE_HEIGHT,
    });
  };

  const addEdge = (source: string, target: string) => {
    const key = `${source}->${target}`;
    if (edgeSet.has(key)) return;
    edgeSet.add(key);
    edges.push({ source, target });
  };

  for (const entry of entries) {
    const servicePortMap = new Map<string, number>();
    for (const svc of entry.services ?? []) {
      if (svc.port) servicePortMap.set(svc.name, svc.port);
    }

    const containerNodeId = `container:${entry.containerName}`;
    const containerColor =
      entry.containerState === "running" ? "#52c41a" : "#ff4d4f";
    addNode(containerNodeId, entry.containerName, containerColor);

    for (const router of entry.routers) {
      for (const ep of router.entrypoints) {
        addNode(`ep:${ep}`, ep, primaryColor);
      }

      const routerDomains =
        router.domains.length > 0 ? router.domains : [];
      const groups = new Set(routerDomains.map(getGroupKey));

      for (const group of groups) {
        const isNetwork = group.includes("/");
        const groupNodeId = `group:${group}`;
        addNode(groupNodeId, group, isNetwork ? "#597ef7" : "#fa8c16");
        for (const ep of router.entrypoints) {
          addEdge(`ep:${ep}`, groupNodeId);
        }
      }

      const routerLabel =
        routerDomains.length > 0 ? routerDomains[0] : router.name;
      const routerNodeId = `router:${router.name}`;
      addNode(routerNodeId, routerLabel, "#13c2c2");

      if (groups.size > 0) {
        for (const group of groups) {
          addEdge(`group:${group}`, routerNodeId);
        }
      } else {
        for (const ep of router.entrypoints) {
          addEdge(`ep:${ep}`, routerNodeId);
        }
      }

      const port = servicePortMap.get(router.serviceName);
      const serviceLabel = port
        ? `${router.serviceName}:${port}`
        : router.serviceName;
      const serviceNodeId = `service:${router.serviceName}`;
      addNode(serviceNodeId, serviceLabel, "#722ed1");

      addEdge(routerNodeId, serviceNodeId);
      addEdge(serviceNodeId, containerNodeId);
    }
  }

  return { nodes: Array.from(nodeMap.values()), edges };
}

/* ------------------------------------------------------------------ */
/*  Dagre layout computation                                           */
/* ------------------------------------------------------------------ */

function computeLayout(
  nodes: GraphNode[],
  edges: GraphEdge[],
): Layout {
  const g = new dagre.graphlib.Graph();
  g.setGraph({
    rankdir: "LR",
    nodesep: 50,
    ranksep: 120,
    marginx: 20,
    marginy: 20,
  });
  g.setDefaultEdgeLabel(() => ({}));

  for (const node of nodes) {
    g.setNode(node.id, { width: node.width, height: node.height });
  }
  for (const edge of edges) {
    g.setEdge(edge.source, edge.target);
  }

  dagre.layout(g);

  const positionedNodes: PositionedNode[] = nodes.map((node) => {
    const n = g.node(node.id);
    return { ...node, x: n.x, y: n.y, width: n.width, height: n.height };
  });

  const layoutEdges: LayoutEdge[] = [];
  for (const edge of edges) {
    const e = g.edge(edge.source, edge.target);
    if (e?.points) {
      layoutEdges.push({ points: e.points });
    }
  }

  const meta = g.graph();
  return {
    nodes: positionedNodes,
    edges: layoutEdges,
    width: (meta.width ?? 800) + 40,
    height: (meta.height ?? 600) + 40,
  };
}

/* ------------------------------------------------------------------ */
/*  Canvas drawing                                                     */
/* ------------------------------------------------------------------ */

function roundRect(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + w - r, y);
  ctx.quadraticCurveTo(x + w, y, x + w, y + r);
  ctx.lineTo(x + w, y + h - r);
  ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
  ctx.lineTo(x + r, y + h);
  ctx.quadraticCurveTo(x, y + h, x, y + h - r);
  ctx.lineTo(x, y + r);
  ctx.quadraticCurveTo(x, y, x + r, y);
  ctx.closePath();
}

function drawEdgePath(
  ctx: CanvasRenderingContext2D,
  points: { x: number; y: number }[],
) {
  if (points.length < 2) return;

  ctx.beginPath();
  ctx.moveTo(points[0].x, points[0].y);

  if (points.length === 2) {
    ctx.lineTo(points[1].x, points[1].y);
  } else {
    for (let i = 1; i < points.length - 1; i++) {
      const xc = (points[i].x + points[i + 1].x) / 2;
      const yc = (points[i].y + points[i + 1].y) / 2;
      ctx.quadraticCurveTo(points[i].x, points[i].y, xc, yc);
    }
    const last = points[points.length - 1];
    ctx.lineTo(last.x, last.y);
  }
  ctx.stroke();

  // Arrow
  const p1 = points[points.length - 2];
  const p2 = points[points.length - 1];
  const angle = Math.atan2(p2.y - p1.y, p2.x - p1.x);
  ctx.beginPath();
  ctx.moveTo(p2.x, p2.y);
  ctx.lineTo(
    p2.x - ARROW_SIZE * Math.cos(angle - Math.PI / 7),
    p2.y - ARROW_SIZE * Math.sin(angle - Math.PI / 7),
  );
  ctx.lineTo(
    p2.x - ARROW_SIZE * Math.cos(angle + Math.PI / 7),
    p2.y - ARROW_SIZE * Math.sin(angle + Math.PI / 7),
  );
  ctx.closePath();
  ctx.fill();
}

function drawAll(
  ctx: CanvasRenderingContext2D,
  layout: Layout,
  transform: ZoomTransform,
  canvasW: number,
  canvasH: number,
  borderColor: string,
) {
  const dpr = window.devicePixelRatio || 1;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, canvasW, canvasH);
  ctx.translate(transform.x, transform.y);
  ctx.scale(transform.k, transform.k);

  // Edges: shared state set once
  ctx.strokeStyle = borderColor;
  ctx.fillStyle = borderColor;
  ctx.lineWidth = 1.5;
  for (const edge of layout.edges) {
    drawEdgePath(ctx, edge.points);
  }

  // Nodes: text state set once
  ctx.font = FONT;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  for (const node of layout.nodes) {
    const x = node.x - node.width / 2;
    const y = node.y - node.height / 2;

    roundRect(ctx, x, y, node.width, node.height, 6);
    ctx.fillStyle = node.color;
    ctx.fill();

    ctx.fillStyle = "#fff";
    ctx.fillText(node.displayLabel, node.x, node.y);
  }
}

/* ------------------------------------------------------------------ */
/*  Hit-test                                                           */
/* ------------------------------------------------------------------ */

function hitTest(
  nodes: PositionedNode[],
  gx: number,
  gy: number,
): PositionedNode | null {
  for (let i = nodes.length - 1; i >= 0; i--) {
    const n = nodes[i];
    const x = n.x - n.width / 2;
    const y = n.y - n.height / 2;
    if (gx >= x && gx <= x + n.width && gy >= y && gy <= y + n.height) {
      return n;
    }
  }
  return null;
}

/* ------------------------------------------------------------------ */
/*  Component                                                          */
/* ------------------------------------------------------------------ */

const CONTAINER_PREFIX = "container:";

export default function DomainsGraph({ data }: Props) {
  const containerRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const ctxRef = useRef<CanvasRenderingContext2D | null>(null);
  const transformRef = useRef<ZoomTransform>(zoomIdentity);
  const tooltipRef = useRef<HTMLDivElement | null>(null);
  const rafRef = useRef(0);
  const navigate = useNavigate();
  const { t } = useTranslation("domains");
  const { token } = theme.useToken();

  const primaryColor = token.colorPrimary;
  const borderColor =
    typeof token.colorBorder === "string" ? token.colorBorder : "#d9d9d9";

  const graphData = useMemo(
    () => (data.length > 0 ? buildGraphData(data, primaryColor) : null),
    [data, primaryColor],
  );

  const layout = useMemo(() => {
    if (!graphData) return null;
    return computeLayout(graphData.nodes, graphData.edges);
  }, [graphData]);

  const scheduleRedraw = useCallback(() => {
    cancelAnimationFrame(rafRef.current);
    rafRef.current = requestAnimationFrame(() => {
      const ctx = ctxRef.current;
      const canvas = canvasRef.current;
      if (!ctx || !canvas || !layout) return;
      const dpr = window.devicePixelRatio || 1;
      drawAll(
        ctx,
        layout,
        transformRef.current,
        canvas.width / dpr,
        canvas.height / dpr,
        borderColor,
      );
    });
  }, [layout, borderColor]);

  useEffect(() => {
    if (!containerRef.current || !layout) return;

    const container = containerRef.current;
    container.innerHTML = "";
    const dpr = window.devicePixelRatio || 1;
    const cw = container.clientWidth;
    const ch = container.clientHeight;

    /* ---- Canvas ---- */
    const canvas = document.createElement("canvas");
    canvas.width = cw * dpr;
    canvas.height = ch * dpr;
    canvas.style.width = `${cw}px`;
    canvas.style.height = `${ch}px`;
    container.appendChild(canvas);
    canvasRef.current = canvas;

    const ctx = canvas.getContext("2d")!;
    ctxRef.current = ctx;

    /* ---- Tooltip ---- */
    const tooltip = document.createElement("div");
    Object.assign(tooltip.style, {
      position: "absolute",
      pointerEvents: "none",
      background: "rgba(0,0,0,.75)",
      color: "#fff",
      padding: "4px 8px",
      borderRadius: "4px",
      fontSize: "12px",
      fontFamily: "system-ui, -apple-system, sans-serif",
      whiteSpace: "nowrap",
      display: "none",
      zIndex: "10",
    } satisfies Partial<CSSStyleDeclaration>);
    container.appendChild(tooltip);
    tooltipRef.current = tooltip;

    /* ---- d3 zoom on the canvas ---- */
    const initialScale = Math.min(cw / layout.width, ch / layout.height, 1);
    const initialTx = (cw - layout.width * initialScale) / 2;
    const initialTy = (ch - layout.height * initialScale) / 2;
    transformRef.current = zoomIdentity
      .translate(initialTx, initialTy)
      .scale(initialScale);

    const zoomBehavior = zoom<HTMLCanvasElement, unknown>()
      .scaleExtent([0.05, 4])
      .on("zoom", (event) => {
        transformRef.current = event.transform;
        scheduleRedraw();
      });

    const canvasSel = select(canvas);
    canvasSel.call(zoomBehavior);
    canvasSel.call(
      zoomBehavior.transform,
      transformRef.current,
    );

    /* ---- Mousemove (tooltip) ---- */
    const handleMouseMove = (e: MouseEvent) => {
      const rect = canvas.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      const t = transformRef.current;
      const gx = (mx - t.x) / t.k;
      const gy = (my - t.y) / t.k;
      const hit = hitTest(layout.nodes, gx, gy);

      if (hit) {
        tooltip.textContent = hit.label;
        tooltip.style.display = "block";
        tooltip.style.left = `${mx + 12}px`;
        tooltip.style.top = `${my - 8}px`;
        canvas.style.cursor = hit.id.startsWith(CONTAINER_PREFIX)
          ? "pointer"
          : "default";
      } else {
        tooltip.style.display = "none";
        canvas.style.cursor = "grab";
      }
    };
    canvas.addEventListener("mousemove", handleMouseMove);
    const handleMouseLeave = () => {
      tooltip.style.display = "none";
    };
    canvas.addEventListener("mouseleave", handleMouseLeave);

    /* ---- Click (container navigation) ---- */
    const handleClick = (e: MouseEvent) => {
      const rect = canvas.getBoundingClientRect();
      const mx = e.clientX - rect.left;
      const my = e.clientY - rect.top;
      const t = transformRef.current;
      const gx = (mx - t.x) / t.k;
      const gy = (my - t.y) / t.k;
      const hit = hitTest(layout.nodes, gx, gy);

      if (hit?.id.startsWith(CONTAINER_PREFIX)) {
        const name = hit.id.slice(CONTAINER_PREFIX.length);
        navigate(`/containers?search=${encodeURIComponent(name)}`);
      }
    };
    canvas.addEventListener("click", handleClick);

    /* ---- Resize ---- */
    const resizeObserver = new ResizeObserver(() => {
      const newCw = container.clientWidth;
      const newCh = container.clientHeight;
      if (newCw === 0 || newCh === 0) return;
      canvas.width = newCw * dpr;
      canvas.height = newCh * dpr;
      canvas.style.width = `${newCw}px`;
      canvas.style.height = `${newCh}px`;
      scheduleRedraw();
    });
    resizeObserver.observe(container);

    /* ---- First render ---- */
    scheduleRedraw();

    return () => {
      resizeObserver.disconnect();
      cancelAnimationFrame(rafRef.current);
      canvas.removeEventListener("mousemove", handleMouseMove);
      canvas.removeEventListener("mouseleave", handleMouseLeave);
      canvas.removeEventListener("click", handleClick);
      container.innerHTML = "";
      canvasRef.current = null;
      ctxRef.current = null;
      tooltipRef.current = null;
    };
  }, [layout, borderColor, scheduleRedraw, navigate]);

  if (data.length === 0) {
    return (
      <div style={{ textAlign: "center", padding: 60 }}>
        <Text type="secondary">{t("graph.empty")}</Text>
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      style={{
        position: "relative",
        width: "100%",
        height: "calc(100vh - 230px)",
        minHeight: 500,
        border: `1px solid ${borderColor}`,
        borderRadius: 8,
        overflow: "hidden",
      }}
    />
  );
}
