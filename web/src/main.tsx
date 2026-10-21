import { render } from "preact";
import { App } from "./App";
import { PrintPage } from "./PrintPage";
import "./style.css";
import "./app.css";

// tech-stack.md T16: the path picks the entry; the fragment carries the token.
function Root() {
  switch (location.pathname) {
    case "/print":
      return <PrintPage />;
    default:
      return <App />;
  }
}

render(<Root />, document.getElementById("app")!);
