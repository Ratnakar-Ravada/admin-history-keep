import {
  Activity,
  ArrowRight,
  Mail,
  MapPin,
  Route,
  ShieldCheck,
} from "lucide-react";

type Props = {
  loginUrl?: string;
  checking?: boolean;
  error?: boolean;
  onRetry: () => void;
};

export function PublicLanding({
  loginUrl = "/auth/login",
  checking,
  error,
  onRetry,
}: Props) {
  return (
    <div className="public-site">
      <a className="public-skip" href="#overview">
        Skip to content
      </a>
      <header className="public-header">
        <a className="public-brand" href="/" aria-label="Location Keeper home">
          <span className="public-brand-icon">
            <MapPin size={22} aria-hidden="true" />
          </span>
          Location Keeper
        </a>
        <nav aria-label="About Location Keeper">
          <a href="#features">Overview</a>
          <a href="#google-access">Google permissions</a>
          <a href="/privacy.html">Privacy policy</a>
        </nav>
      </header>
      <main id="overview">
        <section className="public-hero" aria-labelledby="public-title">
          <div className="public-intro">
            <span className="public-eyebrow">
              A LITTLE CONTEXT. A CLEARER PICTURE.
            </span>
            <h1 id="public-title">
              Your devices.
              <br />
              Their journeys.
              <br />
              <em>One clear view.</em>
            </h1>
            <p className="public-lead">
              Location Keeper brings location history, recorded journeys and
              device activity into one private workspace. Explore the devices
              you own or are authorised to view, and understand when and where
              their reports were recorded.
            </p>
            <div className="public-signin" aria-label="Workspace sign-in">
              {new URLSearchParams(location.search).get("signin") ===
                "failed" && (
                <p className="public-error" role="alert">
                  Google sign-in could not be verified. Choose an authorised
                  account and try again.
                </p>
              )}
              <a className="google-signin" href={loginUrl}>
                <img src="/google-signin.png" alt="Sign in with Google" />
              </a>
              <p className="signin-permissions">
                Sign-in uses your Google name, email and account ID to identify
                you and check workspace access. Gmail access is a separate,
                optional permission. Email alerts are coming soon.
              </p>
              {checking && (
                <p className="public-session-note" role="status">
                  Checking your existing session…
                </p>
              )}
              {error && (
                <p className="public-error" role="status">
                  The workspace is temporarily unavailable. You can still read
                  about the app and its privacy practices.{" "}
                  <button onClick={onRetry}>Retry connection</button>
                </p>
              )}
            </div>
            <a className="public-text-link" href="#features">
              Get to know the workspace{" "}
              <ArrowRight size={16} aria-hidden="true" />
            </a>
          </div>
          <aside className="public-story" aria-label="What you can explore">
            <div className="public-story-heading">
              <MapPin size={25} aria-hidden="true" />
              <span>THE DETAILS, CONNECTED</span>
            </div>
            <h2>
              From a location
              <br />
              to the full story.
            </h2>
            <ol>
              <li>
                <span className="public-step">01</span>
                <div>
                  <h3>Find the latest report</h3>
                  <p>
                    See the latest recorded position alongside its timestamp,
                    accuracy and device context.
                  </p>
                </div>
              </li>
              <li>
                <span className="public-step">02</span>
                <div>
                  <h3>Follow the journey</h3>
                  <p>
                    Explore recorded points on a map and review activity across
                    the dates you choose.
                  </p>
                </div>
              </li>
              <li>
                <span className="public-step">03</span>
                <div>
                  <h3>Understand the gaps</h3>
                  <p>
                    Review reported permission, connectivity and location events
                    in a searchable activity feed.
                  </p>
                </div>
              </li>
            </ol>
            <p className="public-story-note">
              <ShieldCheck size={17} aria-hidden="true" /> Google sign-in alone
              does not grant access to another person’s devices.
            </p>
          </aside>
        </section>

        <section
          className="public-features"
          id="features"
          aria-labelledby="features-title"
        >
          <div className="public-section-heading">
            <span className="public-eyebrow">THE WORKSPACE</span>
            <h2 id="features-title">More context for every recorded moment.</h2>
          </div>
          <div className="public-feature-grid">
            <article>
              <MapPin size={23} aria-hidden="true" />
              <h3>Latest device context</h3>
              <p>
                Review recorded locations, battery and network information.
                Timestamps distinguish the latest report from the latest
                measured position.
              </p>
            </article>
            <article>
              <Route size={23} aria-hidden="true" />
              <h3>Journeys over time</h3>
              <p>
                Filter by device and date, explore recorded journeys on a map
                and replay available location points.
              </p>
            </article>
            <article>
              <Activity size={23} aria-hidden="true" />
              <h3>Activity with an explanation</h3>
              <p>
                Inspect reported events, location availability and
                interruptions, with filters and details for each record.
              </p>
            </article>
          </div>
          <p className="public-boundary">
            Location Keeper displays reports from a separately configured device
            tracking app. This website does not start tracking a device just
            because you visit or sign in.
          </p>
        </section>

        <section
          className="public-permissions"
          id="google-access"
          aria-labelledby="permissions-title"
        >
          <div className="public-section-heading">
            <span className="public-eyebrow">YOUR GOOGLE ACCOUNT</span>
            <h2 id="permissions-title">A clear purpose for each permission.</h2>
            <p>Understand what you share before you continue.</p>
          </div>
          <div className="public-permission-grid">
            <article>
              <ShieldCheck size={24} aria-hidden="true" />
              <h3>Identity for sign-in</h3>
              <p>
                <code>openid</code>, <code>email</code> and <code>profile</code>{" "}
                identify your Google account, provide your name and verified
                email, and help enforce workspace permissions. We never receive
                your Google password.
              </p>
              <a href="/privacy.html#google-permissions">
                How sign-in data is used{" "}
                <ArrowRight size={15} aria-hidden="true" />
              </a>
            </article>
            <article>
              <div className="public-card-heading">
                <Mail size={24} aria-hidden="true" />
                <span className="public-planned">ALERT DELIVERY · PLANNED</span>
              </div>
              <h3>Optional email alerts</h3>
              <p>
                The purpose of <code>gmail.send</code> is only to send app
                alerts from the Google account you connect to recipient email
                addresses you configure in Alerts. It does not give the app
                permission to read your inbox.
              </p>
              <p className="public-feature-status">
                Email alerts are coming soon. Sender connection is available to
                configured administrators; automated sending and recipient
                settings are not active yet. Connecting Gmail does not send a
                message.
              </p>
              <a href="/privacy.html#gmail">
                Read the Gmail disclosure{" "}
                <ArrowRight size={15} aria-hidden="true" />
              </a>
            </article>
          </div>
        </section>
        <section className="public-trust" aria-label="Privacy and access">
          <ShieldCheck size={25} aria-hidden="true" />
          <div>
            <h2>Your workspace has boundaries.</h2>
            <p>
              Device access is restricted to authorised accounts. Account and
              session data support access control, security and internal usage
              analytics. We do not sell Google user data or use it for
              advertising.
            </p>
          </div>
          <a href="/privacy.html">
            Read our privacy policy <ArrowRight size={17} aria-hidden="true" />
          </a>
        </section>
      </main>
      <footer className="public-footer">
        <span>Location Keeper · Operated by Ratnakar Ravada</span>
        <nav aria-label="Privacy and support">
          <a href="/privacy.html">Privacy policy</a>
          <a href="/terms.html">Terms of service</a>
          <a href="mailto:ratnakarravada11@gmail.com">Contact &amp; support</a>
        </nav>
      </footer>
    </div>
  );
}
