# Next.js Sun and Moon Toggle

A minimal Next.js demo for a classic light/dark theme toggle. The control uses CSS-built sun and moon shapes, no emoji, and no external image assets.

## Run

```bash
npm install
npm run dev
```

Open `http://localhost:3000`.

## Validate

```bash
npm run lint
npm run build
```

## Files

- `src/app/page.tsx` contains the interactive button.
- `src/app/globals.css` contains the visual state, sun shape, moon shape, and transitions.

## Accessibility

The button exposes `aria-pressed` and a state-specific `aria-label`. The decorative sun, moon, stars, and craters are hidden from assistive technology with `aria-hidden`.
