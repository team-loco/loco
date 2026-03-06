import { Toaster as Sonner } from "sonner";

type ToasterProps = React.ComponentProps<typeof Sonner>;

const Toaster = ({ ...props }: ToasterProps) => {
	return (
		<>
			<style>{`
        [data-sonner-toaster] {
          --toast-background: hsl(var(--card));
          --toast-border: var(--border);
          --toast-text: hsl(var(--foreground));
        }

        [data-sonner-toast] {
          border: none !important;
          border-radius: 8px !important;
          box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08) !important;
          padding: 16px 20px !important;
          gap: 12px !important;
          background-color: hsl(var(--card)) !important;
          color: hsl(var(--foreground)) !important;
          font-family: inherit !important;
          animation: slideIn 0.2s ease-out !important;
        }

        @keyframes slideIn {
          from {
            transform: translateY(100%) !important;
            opacity: 0 !important;
          }
          to {
            transform: translateY(0) !important;
            opacity: 1 !important;
          }
        }

        @keyframes slideOut {
          from {
            transform: translateY(0) !important;
            opacity: 1 !important;
          }
          to {
            transform: translateY(100%) !important;
            opacity: 0 !important;
          }
        }

        [data-sonner-toast][data-type="success"] {
          background-color: #f0fdf4 !important;
          color: #166534 !important;
        }

        [data-sonner-toast][data-type="success"] [data-sonner-toast-title] {
          color: #166534 !important;
          font-weight: 500 !important;
        }

        [data-sonner-toast][data-type="success"] [data-sonner-toast-description] {
          color: #4b7c5f !important;
          font-weight: 400 !important;
        }

        [data-sonner-toast][data-type="error"] {
          background-color: #fef2f2 !important;
          color: #7f1d1d !important;
        }

        [data-sonner-toast][data-type="error"] [data-sonner-toast-title] {
          color: #7f1d1d !important;
          font-weight: 500 !important;
        }

        [data-sonner-toast][data-type="error"] [data-sonner-toast-description] {
          color: #b34949 !important;
          font-weight: 400 !important;
        }

        [data-sonner-toast][data-type="warning"] {
          background-color: #fffbeb !important;
          color: #92400e !important;
        }

        [data-sonner-toast][data-type="warning"] [data-sonner-toast-title] {
          color: #92400e !important;
          font-weight: 500 !important;
        }

        [data-sonner-toast][data-type="warning"] [data-sonner-toast-description] {
          color: #b88a2c !important;
          font-weight: 400 !important;
        }

        [data-sonner-toast][data-type="info"] {
          background-color: #eff6ff !important;
          color: #0c4a6e !important;
        }

        [data-sonner-toast][data-type="info"] [data-sonner-toast-title] {
          color: #0c4a6e !important;
          font-weight: 500 !important;
        }

        [data-sonner-toast][data-type="info"] [data-sonner-toast-description] {
          color: #4b7a99 !important;
          font-weight: 400 !important;
        }

        [data-sonner-toast] [data-sonner-toast-title] {
          font-size: 14px !important;
          margin: 0 !important;
        }

        [data-sonner-toast] [data-sonner-toast-description] {
          font-size: 13px !important;
          margin: 0 !important;
        }

        [data-sonner-toast-action-button],
        [data-sonner-toast-cancel-button] {
          border: 1px solid currentColor !important;
          border-radius: 6px !important;
          background: transparent !important;
          color: inherit !important;
          font-weight: 500 !important;
          padding: 6px 12px !important;
          cursor: pointer !important;
          transition: all 150ms ease !important;
          font-size: 12px !important;
          opacity: 0.8 !important;
        }

        [data-sonner-toast-action-button]:hover {
          opacity: 1 !important;
          background: currentColor !important;
          color: white !important;
        }

        [data-sonner-toast-action-button]:active {
          transform: scale(0.98) !important;
        }

        [data-sonner-toast-close-button] {
          background: none !important;
          border: none !important;
          color: currentColor !important;
          cursor: pointer !important;
          padding: 2px !important;
          font-size: 18px !important;
          display: flex !important;
          align-items: center !important;
          justify-content: center !important;
          opacity: 0.5 !important;
          transition: opacity 150ms ease !important;
        }

        [data-sonner-toast-close-button]:hover {
          opacity: 0.8 !important;
        }

        @media (prefers-color-scheme: dark) {
          [data-sonner-toast][data-type="success"] {
            background-color: #064e3b !important;
            color: #d1fae5 !important;
          }

          [data-sonner-toast][data-type="success"] [data-sonner-toast-title] {
            color: #d1fae5 !important;
          }

          [data-sonner-toast][data-type="success"] [data-sonner-toast-description] {
            color: #a7f3d0 !important;
          }

          [data-sonner-toast][data-type="error"] {
            background-color: #7f1d1d !important;
            color: #fecaca !important;
          }

          [data-sonner-toast][data-type="error"] [data-sonner-toast-title] {
            color: #fecaca !important;
          }

          [data-sonner-toast][data-type="error"] [data-sonner-toast-description] {
            color: #fca5a5 !important;
          }

          [data-sonner-toast][data-type="warning"] {
            background-color: #78350f !important;
            color: #fcd34d !important;
          }

          [data-sonner-toast][data-type="warning"] [data-sonner-toast-title] {
            color: #fcd34d !important;
          }

          [data-sonner-toast][data-type="warning"] [data-sonner-toast-description] {
            color: #fbbf24 !important;
          }

          [data-sonner-toast][data-type="info"] {
            background-color: #0c2d4d !important;
            color: #93c5fd !important;
          }

          [data-sonner-toast][data-type="info"] [data-sonner-toast-title] {
            color: #93c5fd !important;
          }

          [data-sonner-toast][data-type="info"] [data-sonner-toast-description] {
            color: #60a5fa !important;
          }
        }
      `}</style>
			<Sonner theme="system" richColors className="toaster group" {...props} />
		</>
	);
};

export { Toaster };
