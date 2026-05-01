export const request = async (url: string, options: any = {}) => {
  try {
    const res = await fetch(url, {
      method: options.method || 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: options.body || JSON.stringify({ consumer_token: 'frontend-dashboard', ...options.data }),
    })
    
    if (!res.ok) {
        throw new Error(`HTTP error! status: ${res.status}`)
    }

    const json = await res.json()
    // Code 2000 means success, results can be null or empty array but still "Connected"
    if (json.code === 2000) return { data: json.result, source: 'Connected' }
    
    throw new Error(json.message || 'Unknown error')
  } catch (err) {
    console.error(`Fetch ${url} failed:`, err)
    return { data: null, source: 'Mock fallback' }
  }
}
