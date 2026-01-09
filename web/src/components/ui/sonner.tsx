import { Toaster as Sonner } from "sonner";

type ToasterProps = React.ComponentProps<typeof Sonner>;

const Toaster = ({ ...props }: ToasterProps) => {
	return (
		<>
			<style>{`
        [data-sonner-toaster] {
          --toast-background: hsl(var(--card));
          --toast-border: #000;
          --toast-text: hsl(var(--foreground));
        }

        [data-sonner-toast] {
          border: 2px solid #000 !important;
          border-radius: 16px !important;
          box-shadow: 2px 2px 0px 0px #000 !important;
          padding: 20px 24px !important;
          gap: 16px !important;
          background-color: hsl(var(--card)) !important;
          color: hsl(var(--foreground)) !important;
          font-family: inherit !important;
        }

        [data-sonner-toast][data-type="success"] {
          background-color: #ecfdf5 !important;
        }

        [data-sonner-toast][data-type="error"] {
          background-color: #ffcccc !important;
        }

        [data-sonner-toast][data-type="warning"] {
          background-color: #fffbeb !important;
        }

        [data-sonner-toast][data-type="info"] {
          background-color: #f0f9ff !important;
        }

        [data-sonner-toast] [data-sonner-toast-title] {
          font-weight: 700 !important;
          font-size: 16px !important;
          color: #000 !important;
          margin: 0 !important;
        }

        [data-sonner-toast] [data-sonner-toast-description] {
          font-weight: 400 !important;
          font-size: 14px !important;
          color: #000 !important;
          margin: 0 !important;
        }

        [data-sonner-toast-action-button],
        [data-sonner-toast-cancel-button] {
          border: 2px solid #000 !important;
          border-radius: 12px !important;
          background-color: hsl(var(--primary)) !important;
          color: hsl(var(--primary-foreground)) !important;
          font-weight: 500 !important;
          padding: 8px 16px !important;
          cursor: pointer !important;
          transition: all 75ms ease !important;
          font-size: 14px !important;
          box-shadow: none !important;
        }

        [data-sonner-toast-action-button]:active,
        [data-sonner-toast-cancel-button]:active {
          box-shadow: none !important;
          transform: translate(2px, 2px) !important;
        }

        [data-sonner-toast-action-button]:hover {
          opacity: 0.9 !important;
        }

        [data-sonner-toast-close-button] {
          background: none !important;
          border: none !important;
          color: #000 !important;
          cursor: pointer !important;
          padding: 4px !important;
          font-weight: bold !important;
          font-size: 20px !important;
          display: flex !important;
          align-items: center !important;
          justify-content: center !important;
        }

        [data-sonner-toast-close-button]:hover {
          opacity: 0.7 !important;
        }
      `}</style>
			<Sonner theme="light" className="toaster group" {...props} />
		</>
	);
};

export { Toaster };
