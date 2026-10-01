this looks real ugly. lets see if we can import nikhil-kumar.tk design system into loco
add a theme toggle switch to the user notifications piece and store their preference somewhere in local storage

app gives back not found
fix breadcrumb

done:
- fix sonner nonsense — ui/sonner.tsx was reading useTheme from next-themes while the app
  uses its own ThemeProvider, so the Toaster was stuck on theme="system" and ignored the
  toggle. now reads @/lib/use-theme. see the frontend section of ../notes.md.
