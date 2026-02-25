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
          border: 1px solid var(--border) !important;
          border-radius: 12px !important;
          box-shadow: 0 12px 32px rgba(0, 0, 0, 0.08), 0 4px 16px rgba(0, 0, 0, 0.04) !important;
          padding: 20px 24px !important;
          gap: 16px !important;
          background-color: hsl(var(--card)) !important;
          color: hsl(var(--foreground)) !important;
          font-family: inherit !important;
          backdrop-filter: blur(8px) !important;
          animation: slideIn 0.3s cubic-bezier(0.34, 1.56, 0.64, 1) !important;
        }

        @keyframes slideIn {
          from {
            transform: translateY(100%) translateX(0) !important;
            opacity: 0 !important;
          }
          to {
            transform: translateY(0) translateX(0) !important;
            opacity: 1 !important;
          }
        }

        @keyframes slideOut {
          from {
            transform: translateY(0) translateX(0) !important;
            opacity: 1 !important;
          }
          to {
            transform: translateY(100%) translateX(0) !important;
            opacity: 0 !important;
          }
        }

        [data-sonner-toast][data-type="success"] {
          background: linear-gradient(135deg, #10b981 0%, #059669 100%) !important;
          border-color: #047857 !important;
          color: white !important;
          box-shadow: 0 12px 32px rgba(16, 185, 129, 0.2), 0 4px 16px rgba(16, 185, 129, 0.1) !important;
        }

        [data-sonner-toast][data-type="success"] [data-sonner-toast-title] {
          color: white !important;
        }

        [data-sonner-toast][data-type="success"] [data-sonner-toast-description] {
          color: rgba(255, 255, 255, 0.85) !important;
        }

        [data-sonner-toast][data-type="error"] {
          background: linear-gradient(135deg, #ef4444 0%, #dc2626 100%) !important;
          border-color: #b91c1c !important;
          color: white !important;
          box-shadow: 0 12px 32px rgba(239, 68, 68, 0.2), 0 4px 16px rgba(239, 68, 68, 0.1) !important;
        }

        [data-sonner-toast][data-type="error"] [data-sonner-toast-title] {
          color: white !important;
        }

        [data-sonner-toast][data-type="error"] [data-sonner-toast-description] {
          color: rgba(255, 255, 255, 0.85) !important;
        }

        [data-sonner-toast][data-type="warning"] {
          background: linear-gradient(135deg, #f59e0b 0%, #d97706 100%) !important;
          border-color: #b45309 !important;
          color: white !important;
          box-shadow: 0 12px 32px rgba(245, 158, 11, 0.2), 0 4px 16px rgba(245, 158, 11, 0.1) !important;
        }

        [data-sonner-toast][data-type="warning"] [data-sonner-toast-title] {
          color: white !important;
        }

        [data-sonner-toast][data-type="warning"] [data-sonner-toast-description] {
          color: rgba(255, 255, 255, 0.85) !important;
        }

        [data-sonner-toast][data-type="info"] {
          background: linear-gradient(135deg, #3b82f6 0%, #2563eb 100%) !important;
          border-color: #1d4ed8 !important;
          color: white !important;
          box-shadow: 0 12px 32px rgba(59, 130, 246, 0.2), 0 4px 16px rgba(59, 130, 246, 0.1) !important;
        }

        [data-sonner-toast][data-type="info"] [data-sonner-toast-title] {
          color: white !important;
        }

        [data-sonner-toast][data-type="info"] [data-sonner-toast-description] {
          color: rgba(255, 255, 255, 0.85) !important;
        }

        [data-sonner-toast] [data-sonner-toast-title] {
          font-weight: 700 !important;
          font-size: 15px !important;
          color: var(--foreground) !important;
          margin: 0 !important;
        }

        [data-sonner-toast] [data-sonner-toast-description] {
          font-weight: 400 !important;
          font-size: 13px !important;
          color: var(--muted-foreground) !important;
          margin: 0 !important;
        }

        [data-sonner-toast-action-button],
        [data-sonner-toast-cancel-button] {
          border: none !important;
          border-radius: 8px !important;
          background: rgba(255, 255, 255, 0.25) !important;
          color: white !important;
          font-weight: 600 !important;
          padding: 8px 16px !important;
          cursor: pointer !important;
          transition: all 180ms cubic-bezier(0.34, 1.56, 0.64, 1) !important;
          font-size: 13px !important;
          box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.3), 0 2px 8px rgba(0, 0, 0, 0.1) !important;
          backdrop-filter: blur(4px) !important;
        }

        [data-sonner-toast-action-button]:hover {
          background: rgba(255, 255, 255, 0.35) !important;
          box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.4), 0 4px 12px rgba(0, 0, 0, 0.15) !important;
          transform: translateY(-2px) !important;
        }

        [data-sonner-toast-action-button]:active {
          transform: translateY(0) !important;
        }

        [data-sonner-toast-close-button] {
          background: none !important;
          border: none !important;
          color: rgba(255, 255, 255, 0.7) !important;
          cursor: pointer !important;
          padding: 4px !important;
          font-size: 20px !important;
          display: flex !important;
          align-items: center !important;
          justify-content: center !important;
          border-radius: 6px !important;
          transition: all 180ms ease !important;
        }

        [data-sonner-toast-close-button]:hover {
          background: rgba(255, 255, 255, 0.15) !important;
          color: white !important;
        }

        @media (prefers-color-scheme: dark) {
          [data-sonner-toast] {
            box-shadow: 0 12px 32px rgba(0, 0, 0, 0.3), 0 4px 16px rgba(0, 0, 0, 0.2) !important;
          }

          [data-sonner-toast][data-type="success"] {
            box-shadow: 0 12px 32px rgba(16, 185, 129, 0.3), 0 4px 16px rgba(16, 185, 129, 0.15) !important;
          }

          [data-sonner-toast][data-type="error"] {
            box-shadow: 0 12px 32px rgba(239, 68, 68, 0.3), 0 4px 16px rgba(239, 68, 68, 0.15) !important;
          }

          [data-sonner-toast][data-type="warning"] {
            box-shadow: 0 12px 32px rgba(245, 158, 11, 0.3), 0 4px 16px rgba(245, 158, 11, 0.15) !important;
          }

          [data-sonner-toast][data-type="info"] {
            box-shadow: 0 12px 32px rgba(59, 130, 246, 0.3), 0 4px 16px rgba(59, 130, 246, 0.15) !important;
          }
        }
      `}</style>
			<Sonner theme="system" richColors className="toaster group" {...props} />
		</>
	);
};

export { Toaster };
