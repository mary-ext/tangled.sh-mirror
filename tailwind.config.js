/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./appview/pages/templates/**/*.html"],
  theme: {
    container: {
      padding: "2rem",
      center: true,
      screens: {
        sm: "540px",
        md: "650px",
        lg: "900px",
        xl: "1100px",
        "2xl": "1300x",
      },
    },
    extend: {
      fontFamily: {
        sans: ["Inter", "system-ui", "sans-serif", "ui-sans-serif"],
      },
    },
  },
};
