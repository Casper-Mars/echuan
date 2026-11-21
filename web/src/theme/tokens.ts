// 由 web/scripts/gen-tokens.mjs 从 design/tokens.json 生成，请勿手改。
// 生成命令：node web/scripts/gen-tokens.mjs
//
// 暗色即默认：dark* 是默认解析值，light* 是同一批令牌的亮色解析（供 html[data-theme='light'] 与对照）。
// CSS 侧一律走 tokens.css 的 var(--…)，本文件供 Mantine theme 映射与 JS 侧度量取用。

export const DEFAULT_THEME_MODE = 'dark' as const
export type ThemeMode = 'dark' | 'light'

/** semantic 颜色角色（暗色解析，默认） */
export const semanticDark = {
  "surface-canvas": "#070b14",
  "surface-raised": "#0b1322",
  "surface-overlay": "#0f141e",
  "border-subtle": "#1c2a43",
  "border-default": "#28405f",
  "border-strong": "#3d5a80",
  "text-primary": "#e9f0fb",
  "text-muted": "#93a7c4",
  "text-on-action": "#ffffff",
  "action-primary": "#3d6bff",
  "action-primary-hover": "#5b83ff",
  "action-primary-text": "#9db8ff",
  "focus-ring": "#7fa2ff",
  "feedback-success": "#2ee6a8",
  "feedback-warning": "#ffce4d",
  "feedback-danger": "#ff5d6c",
  "feedback-info": "#6ee6ff",
} as const

/** semantic 颜色角色（亮色解析） */
export const semanticLight = {
  "surface-canvas": "#fcfdff",
  "surface-raised": "#f7fafd",
  "surface-overlay": "#eff4f9",
  "border-subtle": "#d0dbe7",
  "border-default": "#bccadd",
  "border-strong": "#9fb0c6",
  "text-primary": "#182130",
  "text-muted": "#4e6076",
  "text-on-action": "#ffffff",
  "action-primary": "#2f5bea",
  "action-primary-hover": "#2a51d4",
  "action-primary-text": "#2b48b0",
  "focus-ring": "#8aa6f7",
  "feedback-success": "#29a383",
  "feedback-warning": "#ffc53d",
  "feedback-danger": "#e5484d",
  "feedback-info": "#7ce2fe",
} as const

/** primitive 色阶（暗色解析）：Mantine 色阶元组从这里取，页面层不得直接使用 */
export const paletteDark = {
  "jade": {
    "1": "#0d1512",
    "2": "#121c18",
    "3": "#0f2e22",
    "4": "#0b3b2c",
    "5": "#114837",
    "6": "#1b5745",
    "7": "#246854",
    "8": "#2a7e68",
    "9": "#2ee6a8",
    "10": "#27b08b",
    "11": "#1fd8a4",
    "12": "#adf0d4",
  },
  "amber": {
    "1": "#16120c",
    "2": "#1d180f",
    "3": "#302008",
    "4": "#3f2700",
    "5": "#4d3000",
    "6": "#5c3d05",
    "7": "#714f19",
    "8": "#8f6424",
    "9": "#ffce4d",
    "10": "#ffd60a",
    "11": "#ffca16",
    "12": "#ffe7b3",
  },
  "red": {
    "1": "#191111",
    "2": "#201314",
    "3": "#3b1219",
    "4": "#500f1c",
    "5": "#611623",
    "6": "#72232d",
    "7": "#8c333a",
    "8": "#b54548",
    "9": "#ff5d6c",
    "10": "#ec5d5e",
    "11": "#ff9592",
    "12": "#ffd1d9",
  },
  "sky": {
    "1": "#0d141f",
    "2": "#111a27",
    "3": "#112840",
    "4": "#113555",
    "5": "#154467",
    "6": "#1b537b",
    "7": "#1f6692",
    "8": "#197cae",
    "9": "#6ee6ff",
    "10": "#a8eeff",
    "11": "#75c7f0",
    "12": "#c2f3ff",
  },
  "white": "#ffffff",
  "black": "#000000",
  "slate": {
    "1": "#070b14",
    "2": "#0b1322",
    "3": "#0f141e",
    "4": "#e7eef6",
    "5": "#dde6f0",
    "6": "#1c2a43",
    "7": "#28405f",
    "8": "#3d5a80",
    "9": "#7d8fa8",
    "10": "#697b93",
    "11": "#93a7c4",
    "12": "#e9f0fb",
  },
  "brand": {
    "1": "#f5f8ff",
    "2": "#eef3ff",
    "3": "#e2eaff",
    "4": "#d3dfff",
    "5": "#c0d1ff",
    "6": "#a8bfff",
    "7": "#7fa2ff",
    "8": "#6a86ee",
    "9": "#3d6bff",
    "10": "#5b83ff",
    "11": "#9db8ff",
    "12": "#15235c",
  },
} as const

/** primitive 色阶（亮色解析） */
export const paletteLight = {
  "jade": {
    "1": "#fbfefd",
    "2": "#f4fbf7",
    "3": "#e6f7ed",
    "4": "#d6f1e3",
    "5": "#c3e9d7",
    "6": "#acdec8",
    "7": "#8bceb6",
    "8": "#56ba9f",
    "9": "#29a383",
    "10": "#26997b",
    "11": "#208368",
    "12": "#1d3b31",
  },
  "amber": {
    "1": "#fefdfb",
    "2": "#fefbe9",
    "3": "#fff7c2",
    "4": "#ffee9c",
    "5": "#fbe577",
    "6": "#f3d673",
    "7": "#e9c162",
    "8": "#e2a336",
    "9": "#ffc53d",
    "10": "#ffba18",
    "11": "#ab6400",
    "12": "#4f3422",
  },
  "red": {
    "1": "#fffcfc",
    "2": "#fff7f7",
    "3": "#feebec",
    "4": "#ffdbdc",
    "5": "#ffcdce",
    "6": "#fdbdbe",
    "7": "#f4a9aa",
    "8": "#eb8e90",
    "9": "#e5484d",
    "10": "#dc3e42",
    "11": "#ce2c31",
    "12": "#641723",
  },
  "sky": {
    "1": "#f9feff",
    "2": "#f1fafd",
    "3": "#e1f6fd",
    "4": "#d1f0fa",
    "5": "#bee7f5",
    "6": "#a9daed",
    "7": "#8dcae3",
    "8": "#60b3d7",
    "9": "#7ce2fe",
    "10": "#74daf8",
    "11": "#00749e",
    "12": "#1d3e56",
  },
  "white": "#ffffff",
  "black": "#000000",
  "slate": {
    "1": "#fcfdff",
    "2": "#f7fafd",
    "3": "#eff4f9",
    "4": "#e7eef6",
    "5": "#dde6f0",
    "6": "#d0dbe7",
    "7": "#bccadd",
    "8": "#9fb0c6",
    "9": "#7d8fa8",
    "10": "#697b93",
    "11": "#4e6076",
    "12": "#182130",
  },
  "brand": {
    "1": "#f5f8ff",
    "2": "#eef3ff",
    "3": "#e2eaff",
    "4": "#d3dfff",
    "5": "#c0d1ff",
    "6": "#a8bfff",
    "7": "#8aa6f7",
    "8": "#6a86ee",
    "9": "#2f5bea",
    "10": "#2a51d4",
    "11": "#2b48b0",
    "12": "#15235c",
  },
} as const

/** 字体族 */
export const fontFamily = {
  "sans": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  "mono": "\"IBM Plex Mono\", ui-monospace, SFMono-Regular, monospace",
} as const

/** 字阶 */
export const typeScale = {
  "display": {
    "fontSize": "40px",
    "fontWeight": 600,
    "lineHeight": 1.2,
    "letterSpacing": "-0.01em",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "h1": {
    "fontSize": "28px",
    "fontWeight": 600,
    "lineHeight": 1.29,
    "letterSpacing": "-0.01em",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "h2": {
    "fontSize": "22px",
    "fontWeight": 600,
    "lineHeight": 1.36,
    "letterSpacing": "0",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "h3": {
    "fontSize": "18px",
    "fontWeight": 600,
    "lineHeight": 1.44,
    "letterSpacing": "0",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "body": {
    "fontSize": "15px",
    "fontWeight": 400,
    "lineHeight": 1.57,
    "letterSpacing": "0",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "body-strong": {
    "fontSize": "15px",
    "fontWeight": 500,
    "lineHeight": 1.57,
    "letterSpacing": "0",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "small": {
    "fontSize": "13px",
    "fontWeight": 400,
    "lineHeight": 1.54,
    "letterSpacing": "0",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "caption": {
    "fontSize": "12px",
    "fontWeight": 400,
    "lineHeight": 1.5,
    "letterSpacing": "0.01em",
    "fontFamily": "Inter, \"PingFang SC\", \"Hiragino Sans GB\", \"Microsoft YaHei\", system-ui, sans-serif",
  },
  "mono": {
    "fontSize": "13px",
    "fontWeight": 400,
    "lineHeight": 1.54,
    "letterSpacing": "0",
    "fontFamily": "\"IBM Plex Mono\", ui-monospace, SFMono-Regular, monospace",
  },
} as const

/** 间距 */
export const space = {
  "1": "2px",
  "2": "4px",
  "3": "8px",
  "4": "12px",
  "5": "16px",
  "6": "24px",
  "7": "32px",
  "8": "40px",
  "9": "48px",
  "10": "64px",
  "11": "80px",
  "12": "96px",
} as const

/** 圆角 */
export const radius = {
  "0": "0px",
  "2": "2px",
  "4": "4px",
  "6": "6px",
  "8": "8px",
  "12": "12px",
  "16": "16px",
  "pill": "999px",
} as const

/** 阴影 */
export const shadow = {
  "e1": "0px 1px 2px 0px rgb(0 0 0 / 0.06)",
  "e2": "0px 4px 12px 0px rgb(0 0 0 / 0.08)",
  "e3": "0px 12px 32px 0px rgb(0 0 0 / 0.10)",
} as const

/** 动效 */
export const motion = {
  "duration-fast": "120ms",
  "duration-base": "180ms",
  "duration-slow": "240ms",
  "duration-slower": "320ms",
  "easing-standard": "cubic-bezier(0.2, 0, 0, 1)",
  "easing-emphasized": "cubic-bezier(0.2, 0, 0.2, 1.2)",
} as const

/** 断点（px 数值：Mantine breakpoints 需要自己换算成 em） */
export const breakpoint = {
  "sm": 768,
  "md": 1024,
  "lg": 1280,
  "xl": 1600,
} as const

/** z-index 层 */
export const zIndex = {
  "raised": 10,
  "sticky": 100,
  "overlay": 200,
  "dialog": 300,
  "toast": 400,
} as const

/** 结构常量（唯一非 tokens.json 值，见 gen-tokens.mjs 头部说明） */
export const structural = {
  "border-width": "1px",
} as const

/** 令牌名 → CSS 变量名，供组件层拼 var() */
export const cssVar = (name: string) => `var(--${name})`
