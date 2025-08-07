const fetch = require('node-fetch');

/**
 * A map of architectures to their corresponding file names and extensions.
 * This makes it easy to add or change architectures in the future.
 */
const ARCHITECTURES = [
  { from: '/amd64', os: 'linux', arch: 'amd64', ext: 'tar.gz' },
  { from: '/arm64', os: 'linux', arch: 'arm64', ext: 'tar.gz' },
  { from: '/windows', os: 'windows', arch: 'amd64', ext: 'zip' },
  // Add other architectures here as needed
];

/**
 * This function runs at build time to dynamically create redirects.
 * It fetches the latest release tag from GitHub and constructs the download URLs.
 */
async function createLatestReleaseRedirects(ex) {
  console.log('Fetching latest release tag from GitHub...');
  try {
    // We use a HEAD request because we only need the final redirected URL, not the page content.
    // This is much faster and more efficient.
    const response = await fetch('https://github.com/fentas/b/releases/latest', {
      method: 'HEAD',
    });

    // The 'response.url' property will contain the URL after all redirects.
    // e.g., 'https://github.com/fentas/b/releases/tag/v2.3.0'
    const latestUrl = response.url;
    
    // Extract the tag (e.g., 'v2.3.0') from the URL.
    const tag = latestUrl.split('/').pop();

    if (!tag || !tag.startsWith('v')) {
      console.error(`Error: Could not parse a valid release tag from URL: ${latestUrl}`);
      // Return an empty array to prevent build failure.
      return [];
    }
    
    console.log(`Successfully fetched latest release tag: ${tag}`);

    // Construct the base URL for downloads for the latest tag.
    const downloadBaseUrl = `https://github.com/fentas/b/releases/download/${tag}`;

    // Create a redirect object for each architecture.
    const redirects = ARCHITECTURES.map(({ from, os, arch, ext }) => {
      return {
        from: from,
        to: `${downloadBaseUrl}/b-${os}-${arch}.${ext}`,
      };
    });

    console.log('Generated redirects:', redirects);
    return redirects;

  } catch (error) {
    console.error('Failed to fetch latest release from GitHub.', error);
    // Return an empty array on error to ensure the Docusaurus build can complete.
    return [];
  }
}

module.exports = createLatestReleaseRedirects