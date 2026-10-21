import { render } from "preact";
import { DeviceCheck } from "./DeviceCheck";
import { PrintPage } from "./PrintPage";
import "./style.css";

// tech-stack.md T16: the path picks the entry; the fragment carries the token.
function App() {
  switch (location.pathname) {
    case "/print":
      return <PrintPage />;
    default:
      return <DeviceCheck />;
  }
}

render(<App />, document.getElementById("app")!);
