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
          box-shadow: 0 8px 24px rgba(28, 25, 23, 0.08), 0 4px 8px rgba(28, 25, 23, 0.04) !important;
          padding: 20px 24px !important;
          gap: 16px !important;
          background-color: hsl(var(--card)) !important;
          color: hsl(var(--foreground)) !important;
          font-family: inherit !important;
        }

        [data-sonner-toast][data-type="success"] {
          background-color: #EEF3F1 !important;
          border-color: #C5D5CF !important;
        }

        [data-sonner-toast][data-type="error"] {
          background-color: #F8F1F0 !important;
          border-color: #E2C9C6 !important;
        }

        [data-sonner-toast][data-type="warning"] {
          background-color: #F9F6F0 !important;
          border-color: #E5DCCF !important;
        }

        [data-sonner-toast][data-type="info"] {
          background-color: #F0F4F7 !important;
          border-color: #C9D6E2 !important;
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
          border: 1px solid var(--border) !important;
          border-radius: 10px !important;
          background: linear-gradient(135deg, #C7654F 0%, #B55942 100%) !important;
          color: white !important;
          font-weight: 600 !important;
          padding: 8px 16px !important;
          cursor: pointer !important;
          transition: all 180ms ease !important;
          font-size: 13px !important;
          box-shadow: 0 2px 4px rgba(28, 25, 23, 0.04), 0 1px 2px rgba(28, 25, 23, 0.02) !important;
        }

        [data-sonner-toast-action-button]:hover {
          box-shadow: 0 4px 12px rgba(28, 25, 23, 0.06), 0 2px 4px rgba(28, 25, 23, 0.03) !important;
          transform: translateY(-1px) !important;
        }

        [data-sonner-toast-action-button]:active {
          transform: translateY(0) !important;
        }

        [data-sonner-toast-close-button] {
          background: none !important;
          border: none !important;
          color: var(--muted-foreground) !important;
          cursor: pointer !important;
          padding: 4px !important;
          font-size: 20px !important;
          display: flex !important;
          align-items: center !important;
          justify-content: center !important;
          border-radius: 6px !important;
          transition: all 180ms !important;
        }

        [data-sonner-toast-close-button]:hover {
          background: var(--accent) !important;
          color: var(--foreground) !important;
        }
      `}</style>
			<Sonner theme="light" className="toaster group" {...props} />
		</>
	);
};

export { Toaster };
