/** @type {import('tailwindcss').Config} */
export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0', transform: 'translateY(2px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.75)' },
          '100%': { opacity: '1', transform: 'scale(1)' },
        },
        backdropIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        backdropOut: {
          '0%': { opacity: '1' },
          '100%': { opacity: '0' },
        },
        modalIn: {
          '0%': { opacity: '0', transform: 'scale(0.96) translateY(6px)' },
          '100%': { opacity: '1', transform: 'scale(1) translateY(0)' },
        },
        modalOut: {
          '0%': { opacity: '1', transform: 'scale(1) translateY(0)' },
          '100%': { opacity: '0', transform: 'scale(0.96) translateY(6px)' },
        },
        slideInRight: {
          '0%': { transform: 'translateX(100%)' },
          '100%': { transform: 'translateX(0)' },
        },
        slideOutRight: {
          '0%': { transform: 'translateX(0)' },
          '100%': { transform: 'translateX(100%)' },
        },
        dropdownIn: {
          '0%': { opacity: '0', transform: 'scaleY(0.95) translateY(-4px)' },
          '100%': { opacity: '1', transform: 'scaleY(1) translateY(0)' },
        },
        tabIn: {
          '0%': { opacity: '0', transform: 'translateY(5px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
      },
      animation: {
        'fade-in':        'fadeIn 280ms ease-out both',
        'scale-in':       'scaleIn 400ms cubic-bezier(0.34, 1.56, 0.64, 1) both',
        'backdrop-in':    'backdropIn 160ms ease-out both',
        'backdrop-out':   'backdropOut 180ms ease-in both',
        'modal-in':       'modalIn 220ms cubic-bezier(0.16, 1, 0.3, 1) both',
        'modal-out':      'modalOut 200ms ease-in both',
        'slide-in-right': 'slideInRight 280ms cubic-bezier(0.16, 1, 0.3, 1) both',
        'slide-out-right':'slideOutRight 260ms cubic-bezier(0.4, 0, 1, 1) both',
        'dropdown-in':    'dropdownIn 140ms cubic-bezier(0.16, 1, 0.3, 1) both',
        'tab-in':         'tabIn 160ms cubic-bezier(0.16, 1, 0.3, 1) both',
      },
    },
  },
  plugins: [],
}
