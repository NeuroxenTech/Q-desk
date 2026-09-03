/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./app/**/*.{js,ts,jsx,tsx,mdx}",
    "./components/**/*.{js,ts,jsx,tsx,mdx}",
    "./lib/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        canvas: {
          DEFAULT: "#0a0e1a",
          alt: "#0d1220",
        },
        panel: {
          DEFAULT: "#10182b",
          border: "#1e293b",
        },
        bar: {
          DEFAULT: "#0c1220",
          border: "#1e293b",
        },
        primary: {
          DEFAULT: "#2563eb",
          light: "#3b82f6",
          dark: "#1d4ed8",
        },
        "text-primary": "#e2e8f0",
        "text-secondary": "#64748b",
        "text-muted": "#94a3b8",
        success: "#22c55e",
        warning: "#f59e0b",
        danger: {
          DEFAULT: "#ef4444",
          bright: "#dc2626",
          deep: "#991b1b",
        },
        surface: {
          DEFAULT: "#0d1220",
          raised: "#10182b",
          overlay: "#1e293b",
        },
      },
    },
  },
  plugins: [],
};
