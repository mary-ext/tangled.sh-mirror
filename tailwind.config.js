/** @type {import('tailwindcss').Config} */
const colors = require("tailwindcss/colors");

module.exports = {
	content: ["./appview/pages/templates/**/*.html", "./appview/pages/chroma.go"],
	darkMode: "media",
	theme: {
		container: {
			padding: "2rem",
			center: true,
			screens: {
				sm: "500px",
				md: "600px",
				lg: "800px",
				xl: "1000px",
				"2xl": "1200px",
			},
		},
		extend: {
			fontFamily: {
				sans: ["InterVariable", "system-ui", "sans-serif", "ui-sans-serif"],
				mono: [
					"IBMPlexMono",
					"ui-monospace",
					"SFMono-Regular",
					"Menlo",
					"Monaco",
					"Consolas",
					"Liberation Mono",
					"Courier New",
					"monospace",
				],
			},
			typography: {
				DEFAULT: {
					css: {
						maxWidth: "none",
						pre: {
							backgroundColor: colors.gray[100],
							color: colors.black,
							"@apply font-normal text-black bg-gray-100 dark:bg-gray-900 dark:text-gray-300 dark:border-gray-700 dark:border": {},
						},
						code: {
							"@apply font-normal font-mono p-1 rounded text-black bg-gray-100 dark:bg-gray-900 dark:text-gray-300 dark:border-gray-700": {},
						},
						"code::before": {
							content: '""',
							"padding-left": "0.25rem"
						},
						"code::after": {
							content: '""',
							"padding-right": "0.25rem"
						},
						blockquote: {
							quotes: "none",
						},
					},
				},
			},
		},
	},
	plugins: [require("@tailwindcss/typography")],
};
