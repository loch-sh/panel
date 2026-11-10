import { memo, useMemo } from "react";

interface SparklineProps {
  data: number[];
  width?: number;
  height?: number;
  color?: string;
  max?: number;
}

/**
 * Minimal SVG sparkline component — no external dependencies.
 * Renders a smooth area chart from an array of values.
 */
const Sparkline = ({
  data,
  width = 120,
  height = 30,
  color = "#1677ff",
  max: maxOverride,
}: SparklineProps) => {
  const { linePath, areaPath } = useMemo(() => {
    if (data.length < 2) return { linePath: "", areaPath: "" };

    const max = maxOverride ?? Math.max(...data, 1);
    const range = max || 1;
    const stepX = width / (data.length - 1);

    const points = data.map((v, i) => {
      const x = i * stepX;
      const y = height - (v / range) * height;
      return `${x},${y}`;
    });

    return {
      linePath: points.join(" "),
      areaPath: `M0,${height} L${points.join(" L")} L${width},${height} Z`,
    };
  }, [data, width, height, maxOverride]);

  if (!linePath) {
    return <svg width={width} height={height} />;
  }

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      style={{ display: "block" }}
    >
      <path d={areaPath} fill={color} fillOpacity={0.15} />
      <polyline
        points={linePath}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
};

export default memo(Sparkline);
