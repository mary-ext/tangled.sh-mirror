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
				"2xl": "1300px"
			},
		},
		extend: {
			fontFamily: {
				sans: ["iA Writer Quattro S", "Inter", "system-ui", "sans-serif", "ui-sans-serif"],
				mono: ["iA Writer Mono S", "ui-monospace", "SFMono-Regular", "Menlo", "Monaco", "Consolas", "Liberation Mono", "Courier New", "monospace"],
			},
			typography: {
				DEFAULT: {
					css: {
						maxWidth: 'none',
					}
				},
			},
		},
	},
	plugins: [
		require('@tailwindcss/typography'),
	]
};
